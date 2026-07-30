package cloudevents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/protocol/http"
	"github.com/google/uuid"
)

type recordsPayload struct {
	Records []json.RawMessage `json:"Records"`
}

func newCloudEvent(bucketName string, records []json.RawMessage) (cloudevents.Event, error) {
	payload := recordsPayload{Records: records}

	e := cloudevents.NewEvent()
	e.SetType("com.noobaa.s3.notification")
	e.SetSource("noobaa/" + bucketName)
	e.SetID(uuid.New().String())
	e.SetTime(time.Now())
	if err := e.SetData(cloudevents.ApplicationJSON, payload); err != nil {
		return e, fmt.Errorf("setting event data: %w", err)
	}
	return e, nil
}

func DispatchEvent(ctx context.Context, targetURI string, bucketName string, records []json.RawMessage) error {
	e, err := newCloudEvent(bucketName, records)
	if err != nil {
		return err
	}

	c, err := cloudevents.NewClientHTTP(http.WithTarget(targetURI))
	if err != nil {
		return fmt.Errorf("creating cloudevent client for %s: %w", targetURI, err)
	}

	ctx = cloudevents.WithEncodingStructured(ctx)
	result := c.Send(ctx, e)
	if cloudevents.IsUndelivered(result) {
		return fmt.Errorf("failed to send CloudEvent to %s: %v", targetURI, result)
	}
	return nil
}

func NewKafkaProducer(brokers []string, config *sarama.Config) (sarama.SyncProducer, error) {
	return sarama.NewSyncProducer(brokers, config)
}

func DispatchEventToKafka(ctx context.Context, producer sarama.SyncProducer, topic string, bucketName string, records []json.RawMessage) error {
	e, err := newCloudEvent(bucketName, records)
	if err != nil {
		return err
	}

	eventJSON, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshaling cloud event: %w", err)
	}

	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(bucketName),
		Value: sarama.ByteEncoder(eventJSON),
	}

	if _, _, err := producer.SendMessage(msg); err != nil {
		return fmt.Errorf("sending message to kafka topic %s: %w", topic, err)
	}
	return nil
}
