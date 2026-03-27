package mq

import (
	"context"
	"encoding/json"
	"time"

	"pim/internal/config"
	logkit "pim/internal/log/kit"
	logmodel "pim/internal/log/model"
	"pim/internal/kit/mq/kafka"
)

func emitConsumerLog(producer *kafka.Producer, entry logmodel.Log) {
	if producer == nil {
		return
	}
	if entry.TS.IsZero() {
		entry.TS = time.Now()
	}
	entry, ok := logkit.ApplyPolicy(entry, config.LogInfoSamplePct)
	if !ok {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = producer.SendMessage(context.Background(), "log-topic", "", data)
}

func minUint(a, b uint) uint {
	if a < b {
		return a
	}
	return b
}

func maxUint(a, b uint) uint {
	if a > b {
		return a
	}
	return b
}
