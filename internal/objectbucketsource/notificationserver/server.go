package notificationserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/IBM/sarama"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ceDispatch "github.com/functions-dev/func-operator/internal/objectbucketsource/cloudevents"
)

var log = logf.Log.WithName("notification-server")

type NotificationServer struct {
	Client                    client.Client
	Port                      int
	KafkaBrokers              []string
	KafkaConfig               *sarama.Config
	NotificationsMode         string
	KafkaNotificationsTopics  []string
	KafkaNotificationsGroupID string
}

func (s *NotificationServer) Start(ctx context.Context) error {
	var kafkaProducer sarama.SyncProducer
	if len(s.KafkaBrokers) > 0 {
		var err error
		kafkaProducer, err = ceDispatch.NewKafkaProducer(s.KafkaBrokers, s.KafkaConfig)
		if err != nil {
			return fmt.Errorf("creating kafka producer: %w", err)
		}
		defer func() { _ = kafkaProducer.Close() }()
		log.Info("kafka producer initialized", "brokers", s.KafkaBrokers)
	}

	handler := &notificationHandler{client: s.Client, kafkaProducer: kafkaProducer}

	if s.NotificationsMode == "kafka" {
		return s.startKafkaConsumer(ctx, handler)
	}
	return s.startHTTPServer(ctx, handler)
}

func (s *NotificationServer) startHTTPServer(ctx context.Context, handler *notificationHandler) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handler.handleNotification)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.Port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error(err, "notification server shutdown error")
		}
	}()

	log.Info("starting notification HTTP server", "port", s.Port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("notification server: %w", err)
	}
	return nil
}

func (s *NotificationServer) startKafkaConsumer(ctx context.Context, handler *notificationHandler) error {
	consumerConfig := *s.KafkaConfig
	consumerConfig.Consumer.Return.Errors = true
	consumerConfig.Consumer.Offsets.Initial = sarama.OffsetNewest

	consumerGroup, err := sarama.NewConsumerGroup(s.KafkaBrokers, s.KafkaNotificationsGroupID, &consumerConfig)
	if err != nil {
		return fmt.Errorf("creating kafka consumer group: %w", err)
	}
	defer func() { _ = consumerGroup.Close() }()

	log.Info("starting kafka notification consumer",
		"topics", s.KafkaNotificationsTopics,
		"group", s.KafkaNotificationsGroupID,
		"brokers", s.KafkaBrokers)

	go func() {
		for err := range consumerGroup.Errors() {
			log.Error(err, "kafka consumer group error")
		}
	}()

	cgHandler := &consumerGroupHandler{handler: handler}

	for {
		if err := consumerGroup.Consume(ctx, s.KafkaNotificationsTopics, cgHandler); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Error(err, "kafka consumer group session error, restarting")
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func (s *NotificationServer) NeedLeaderElection() bool {
	return false
}
