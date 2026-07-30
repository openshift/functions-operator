package notificationserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/IBM/sarama"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sourcesv1alpha1 "github.com/functions-dev/func-operator/api/sources/v1alpha1"
	ceDispatch "github.com/functions-dev/func-operator/internal/objectbucketsource/cloudevents"
	"github.com/functions-dev/func-operator/internal/objectbucketsource/eventmatch"
)

type notificationHandler struct {
	client        client.Client
	kafkaProducer sarama.SyncProducer
}

type notificationPayload struct {
	Records []json.RawMessage `json:"Records"`
}

type recordHeader struct {
	Event     string   `json:"Event,omitempty"`
	EventName string   `json:"eventName,omitempty"`
	Bucket    string   `json:"Bucket,omitempty"`
	S3        *s3Field `json:"s3,omitempty"`
}

type s3Field struct {
	Bucket s3Bucket `json:"bucket"`
}

type s3Bucket struct {
	Name string `json:"name"`
}

type parsedRecord struct {
	raw    json.RawMessage
	header recordHeader
}

func (h *notificationHandler) handleNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error(err, "reading request body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	if err := h.processNotification(r.Context(), body); err != nil {
		log.Error(err, "processing notification")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *notificationHandler) processNotification(ctx context.Context, body []byte) error {
	var payload notificationPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parsing notification payload: %w", err)
	}

	var dataRecords []parsedRecord
	for _, rawRecord := range payload.Records {
		var header recordHeader
		if err := json.Unmarshal(rawRecord, &header); err != nil {
			log.Error(err, "parsing record header")
			continue
		}

		if header.Event == "s3:TestEvent" {
			h.handleTestEvent(ctx, header.Bucket)
		} else if header.EventName != "" && header.S3 != nil {
			dataRecords = append(dataRecords, parsedRecord{raw: rawRecord, header: header})
		}
	}

	if len(dataRecords) > 0 {
		h.dispatchDataEvents(ctx, dataRecords)
	}

	return nil
}

func (h *notificationHandler) handleTestEvent(ctx context.Context, bucketName string) {
	sources, err := h.findSourcesForBucket(ctx, bucketName)
	if err != nil {
		log.Error(err, "finding sources for test event", "bucket", bucketName)
		return
	}

	for i := range sources {
		source := &sources[i]
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := h.client.Get(ctx, client.ObjectKeyFromObject(source), source); err != nil {
				return err
			}
			meta.SetStatusCondition(&source.Status.Conditions, metav1.Condition{
				Type:               sourcesv1alpha1.ConditionTestEventReceived,
				Status:             metav1.ConditionTrue,
				Reason:             "TestEventReceived",
				Message:            "Received test event from NooBaa",
				ObservedGeneration: source.Generation,
			})
			return h.client.Status().Update(ctx, source)
		}); err != nil {
			log.Error(err, "updating TestEventReceived condition", "source", source.Name, "namespace", source.Namespace)
		} else {
			log.Info("set TestEventReceived=True", "source", source.Name, "namespace", source.Namespace)
		}
	}
}

func (h *notificationHandler) dispatchDataEvents(ctx context.Context, records []parsedRecord) {
	var allSources sourcesv1alpha1.ObjectBucketSourceList
	if err := h.client.List(ctx, &allSources); err != nil {
		log.Error(err, "listing sources for data events")
		return
	}

	for _, source := range allSources.Items {
		var matchedRecords []json.RawMessage
		obcName := source.Spec.ObjectBucketClaim.Name
		for _, rec := range records {
			if rec.header.S3.Bucket.Name != obcName {
				continue
			}
			for _, pattern := range source.Spec.Events {
				if eventmatch.MatchEvent(pattern, rec.header.EventName) {
					matchedRecords = append(matchedRecords, rec.raw)
					break
				}
			}
		}

		if len(matchedRecords) == 0 {
			continue
		}

		sinkURI := source.Spec.Sink.URI
		if strings.HasPrefix(sinkURI, "kafka:") {
			topic := strings.TrimPrefix(sinkURI, "kafka:")
			if h.kafkaProducer == nil {
				log.Error(fmt.Errorf("no kafka brokers configured"), "cannot dispatch to kafka", "topic", topic, "bucket", obcName)
				continue
			}
			if err := ceDispatch.DispatchEventToKafka(ctx, h.kafkaProducer, topic, obcName, matchedRecords); err != nil {
				log.Error(err, "dispatching event to kafka", "topic", topic, "bucket", obcName)
			} else {
				log.Info("dispatched event to kafka", "topic", topic, "bucket", obcName, "records", len(matchedRecords))
			}
		} else {
			if err := ceDispatch.DispatchEvent(ctx, sinkURI, obcName, matchedRecords); err != nil {
				log.Error(err, "dispatching event", "target", sinkURI, "bucket", obcName)
			} else {
				log.Info("dispatched event", "target", sinkURI, "bucket", obcName, "records", len(matchedRecords))
			}
		}
	}
}

func (h *notificationHandler) findSourcesForBucket(ctx context.Context, bucketName string) ([]sourcesv1alpha1.ObjectBucketSource, error) {
	var allSources sourcesv1alpha1.ObjectBucketSourceList
	if err := h.client.List(ctx, &allSources); err != nil {
		return nil, err
	}

	var matched []sourcesv1alpha1.ObjectBucketSource
	for _, s := range allSources.Items {
		if s.Spec.ObjectBucketClaim.Name == bucketName {
			matched = append(matched, s)
		}
	}
	return matched, nil
}
