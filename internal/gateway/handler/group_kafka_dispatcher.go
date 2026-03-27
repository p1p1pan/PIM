package handler

import (
	"context"
	"errors"
	"hash/fnv"
	"log"
	"time"

	"pim/internal/config"
	"pim/internal/kit/mq/kafka"
)

type groupKafkaTask struct {
	key   string
	value []byte
	done  chan error
}

// groupKafkaDispatcher 将群消息按 key 固定分配到 worker：
// - 同 key（同 group）总是进入同一 worker，保证入 Kafka 的顺序稳定；
// - 不同 key 可分散到多个 worker 并行发送，提高吞吐。
type groupKafkaDispatcher struct {
	workers []*groupKafkaWorker
}

func newGroupKafkaDispatcher(producer *kafka.Producer) *groupKafkaDispatcher {
	if producer == nil {
		return nil
	}
	parallel := config.GatewayGroupKafkaParallel
	if parallel <= 0 {
		parallel = 8
	}
	workers := make([]*groupKafkaWorker, 0, parallel)
	for i := 0; i < parallel; i++ {
		w := newGroupKafkaWorker(producer)
		workers = append(workers, w)
	}
	d := &groupKafkaDispatcher{workers: workers}
	return d
}

func (d *groupKafkaDispatcher) stop() {
	if d == nil || len(d.workers) == 0 {
		return
	}
	for _, w := range d.workers {
		w.stop()
	}
}

func (d *groupKafkaDispatcher) submit(ctx context.Context, key string, value []byte) error {
	if d == nil || len(d.workers) == 0 {
		return errors.New("group kafka dispatcher not initialized")
	}
	idx := hashGroupKafkaKey(key) % uint32(len(d.workers))
	return d.workers[idx].submit(ctx, key, value)
}

type groupKafkaWorker struct {
	producer *kafka.Producer
	ch       chan groupKafkaTask
	stopCh   chan struct{}
}

func newGroupKafkaWorker(producer *kafka.Producer) *groupKafkaWorker {
	queueSize := config.GatewayGroupKafkaQueueSize
	if queueSize <= 0 {
		queueSize = 8192
	}
	w := &groupKafkaWorker{
		producer: producer,
		ch:       make(chan groupKafkaTask, queueSize),
		stopCh:   make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *groupKafkaWorker) stop() {
	if w == nil {
		return
	}
	close(w.stopCh)
}

// submit 将消息提交到 worker 队列。
// - waitResult=false: 只保证“入队成功”，快速返回（低延迟入口路径）；
// - waitResult=true: 等待本批 flush 结果，适合需要同步感知发送结果的模式。
func (w *groupKafkaWorker) submit(ctx context.Context, key string, value []byte) error {
	waitResult := config.GatewayGroupKafkaWaitResult
	task := groupKafkaTask{
		key:   key,
		value: value,
	}
	if waitResult {
		task.done = make(chan error, 1)
	}
	enqueueTimeout := time.Duration(config.GatewayGroupKafkaEnqueueTimeoutMs) * time.Millisecond
	if enqueueTimeout <= 0 {
		enqueueTimeout = 80 * time.Millisecond
	}
	waitTimeout := time.Duration(config.GatewayGroupKafkaWaitTimeoutMs) * time.Millisecond
	if waitTimeout <= 0 {
		waitTimeout = 300 * time.Millisecond
	}
	enqueueTimer := time.NewTimer(enqueueTimeout)
	defer enqueueTimer.Stop()
	select {
	case w.ch <- task:
	case <-ctx.Done():
		return ctx.Err()
	case <-enqueueTimer.C:
		return errors.New("group kafka enqueue timeout")
	}
	if !waitResult {
		// 低延迟模式：仅保证任务成功入队，发送结果由 worker 异步处理。
		return nil
	}
	waitTimer := time.NewTimer(waitTimeout)
	defer waitTimer.Stop()
	select {
	case err := <-task.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-waitTimer.C:
		return errors.New("group kafka dispatch wait timeout")
	}
}

// run 是 worker 主循环：
// - 按 batchSize / batchWait 聚合消息并批量写入 Kafka；
// - stop 时会先 flush 缓冲区再退出，尽量减少进程关闭时的消息滞留。
func (w *groupKafkaWorker) run() {
	batchSize := config.GatewayGroupKafkaBatchSize
	if batchSize <= 0 {
		batchSize = 64
	}
	wait := time.Duration(config.GatewayGroupKafkaBatchWaitMs) * time.Millisecond
	if wait <= 0 {
		wait = 3 * time.Millisecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	buf := make([]groupKafkaTask, 0, batchSize)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		msgs := make([]kafka.TopicMessage, 0, len(buf))
		for _, t := range buf {
			msgs = append(msgs, kafka.TopicMessage{
				Topic: "group-message",
				Key:   t.key,
				Value: t.value,
			})
		}
		flushTimeout := time.Duration(config.GatewayGroupKafkaFlushTimeoutMs) * time.Millisecond
		if flushTimeout <= 0 {
			flushTimeout = 2 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
		err := w.producer.SendTopicMessages(ctx, msgs)
		cancel()
		for _, t := range buf {
			if t.done != nil {
				t.done <- err
			}
		}
		// 异步模式下，发送失败只记录日志，不阻塞入口请求。
		if err != nil {
			log.Printf("group kafka worker flush failed: batch=%d err=%v", len(buf), err)
		}
		buf = buf[:0]
	}

	for {
		select {
		case <-w.stopCh:
			flush()
			return
		case t := <-w.ch:
			buf = append(buf, t)
			if len(buf) >= batchSize {
				flush()
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(wait)
			}
		case <-timer.C:
			flush()
			timer.Reset(wait)
		}
	}
}

func hashGroupKafkaKey(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}
