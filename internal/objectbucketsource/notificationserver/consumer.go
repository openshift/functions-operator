package notificationserver

import (
	"github.com/IBM/sarama"
)

type consumerGroupHandler struct {
	handler *notificationHandler
}

func (h *consumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error {
	log.Info("kafka consumer group session setup")
	return nil
}

func (h *consumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error {
	log.Info("kafka consumer group session cleanup")
	return nil
}

func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		log.Info("received kafka notification",
			"topic", msg.Topic,
			"partition", msg.Partition,
			"offset", msg.Offset)

		if err := h.handler.processNotification(session.Context(), msg.Value); err != nil {
			log.Error(err, "processing kafka notification",
				"topic", msg.Topic,
				"partition", msg.Partition,
				"offset", msg.Offset)
		}

		session.MarkMessage(msg, "")
	}
	return nil
}
