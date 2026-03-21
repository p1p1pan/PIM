package mq

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	logmodel "pim/internal/log/model"
	logstore "pim/internal/log/store"
	"pim/internal/mq/kafka"

	"github.com/Shopify/sarama"
)

// StartConsumers 启动 log-topic 消费并写入 ES。
func StartConsumers(ctx context.Context, es *logstore.ESStore, brokers []string, topic string) {
	if err := kafka.StartSimpleConsumer(ctx, brokers, topic, func(ctx context.Context, msg *sarama.ConsumerMessage) error {
		var e logmodel.Log
		if err := json.Unmarshal(msg.Value, &e); err != nil {
			log.Printf("log-topic invalid message: %v", err)
			return nil
		}
		if e.TS.IsZero() {
			e.TS = time.Now()
		}
		e.Level = strings.ToLower(strings.TrimSpace(e.Level))
		if e.Level == "" {
			e.Level = "info"
		}
		if e.Service == "" || e.TraceID == "" || e.Msg == "" {
			log.Printf("log-topic skip incomplete entry service=%q trace=%q msg=%q", e.Service, e.TraceID, e.Msg)
			return nil
		}
		if err := es.Index(ctx, e); err != nil {
			log.Printf("log-topic index failed trace=%s event=%s err=%v", e.TraceID, e.EventID, err)
			return err
		}
		return nil
	}); err != nil {
		log.Printf("log-topic consumer start failed: %v", err)
	}
}
