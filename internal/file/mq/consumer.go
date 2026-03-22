package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"pim/internal/config"
	filemodel "pim/internal/file/model"
	fileservice "pim/internal/file/service"
	logkit "pim/internal/log/kit"
	logmodel "pim/internal/log/model"
	"pim/internal/mq/kafka"

	"github.com/Shopify/sarama"
)

// StartConsumers 启动 file-scan 消费。
func StartConsumers(ctx context.Context, svc *fileservice.Service, producer *kafka.Producer, brokers []string) {
	// 使用通用简单消费者模型，阶段4先保证链路完整性。
	if err := kafka.StartSimpleConsumer(ctx, brokers, "file-scan", func(ctx context.Context, msg *sarama.ConsumerMessage) error {
		var evt filemodel.FileScanEvent
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Printf("file-scan invalid message: %v", err)
			emitConsumerLog(producer, logmodel.Log{
				TS:        time.Now(),
				Level:     "error",
				Service:   "file",
				TraceID:   "unknown",
				Msg:       "file-scan decode failed",
				ErrorCode: "decode_failed",
			})
			return nil
		}
		traceID := evt.TraceID
		if traceID == "" {
			traceID = "unknown"
		}
		eventID := "file-scan:" + strconv.FormatUint(uint64(evt.FileID), 10)
		// 阶段4：模拟扫描结果固定为 ok，后续可替换为真实扫描引擎回调。
		if _, err := svc.ApplyScanResult(evt.FileID, "ok", ""); err != nil {
			log.Printf("file-scan apply result failed file_id=%d err=%v", evt.FileID, err)
			if evt.Retry < config.FileScanMaxRetry {
				// 重试次数未耗尽：记录下一次重试时间并延迟重新投递。
				next := evt
				next.Retry = evt.Retry + 1
				delay := calcBackoff(next.Retry)
				nextRetryAt := time.Now().Add(delay)
				_ = svc.UpdateScanRetry(evt.FileID, next.Retry, err.Error(), nextRetryAt)
				if sendErr := scheduleRetry(ctx, producer, next, delay); sendErr != nil {
					log.Printf("file-scan retry schedule failed file_id=%d retry=%d err=%v", evt.FileID, next.Retry, sendErr)
				}
			} else {
				// 超过最大重试：标记任务为 dead_letter，并投递到 DLQ topic 供人工处理。
				_, _ = svc.MarkScanFailed(evt.FileID, "scan_retry_exceeded")
				dlqData, _ := json.Marshal(evt)
				_ = producer.SendMessage(ctx, config.FileScanDLQTopic, "", dlqData)
			}
			emitConsumerLog(producer, logmodel.Log{
				TS:        time.Now(),
				Level:     "error",
				Service:   "file",
				TraceID:   traceID,
				Msg:       "file-scan apply failed",
				EventID:   eventID,
				ErrorCode: "scan_apply_failed",
			})
			return nil
		}
		emitConsumerLog(producer, logmodel.Log{
			TS:      time.Now(),
			Level:   "info",
			Service: "file",
			TraceID: traceID,
			Msg:     "file-scan consumed",
			EventID: eventID,
		})
		return nil
	}); err != nil {
		log.Printf("file-scan consumer start failed: %v", err)
	}
}

func calcBackoff(retry int) time.Duration {
	if retry <= 0 {
		retry = 1
	}
	base := config.FileScanRetryBaseSec
	if base <= 0 {
		base = 1
	}
	maxSec := config.FileScanRetryMaxSec
	if maxSec <= 0 {
		maxSec = 30
	}
	sec := base
	for i := 1; i < retry; i++ {
		sec *= 2
		if sec >= maxSec {
			sec = maxSec
			break
		}
	}
	return time.Duration(sec) * time.Second
}

func scheduleRetry(ctx context.Context, producer *kafka.Producer, evt filemodel.FileScanEvent, delay time.Duration) error {
	if producer == nil {
		return fmt.Errorf("producer not initialized")
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// 延迟到期后重新投递原 topic，让同一消费流程继续处理。
			data, _ := json.Marshal(evt)
			if err := producer.SendMessage(context.Background(), "file-scan", "", data); err != nil {
				log.Printf("file-scan retry publish failed file_id=%d retry=%d err=%v", evt.FileID, evt.Retry, err)
			}
		}
	}()
	return nil
}

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
