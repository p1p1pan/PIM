package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/Shopify/sarama"

	groupmodel "pim/internal/group/model"
	"pim/internal/mq/kafka"
)

// StartGroupMemberSyncConsumer 启动 gateway 本地成员索引同步消费者。
// 每个 gateway 节点使用独立 consumer group，确保都能收到全量成员变更事件。
func StartGroupMemberSyncConsumer(ctx context.Context, brokers []string, nodeID string) {
	groupID := fmt.Sprintf("gateway-group-member-sync-%s", nodeID)
	go func() {
		err := kafka.StartConsumerGroup(ctx, brokers, groupID, []string{"group-member-sync"}, func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			var evt groupmodel.GroupMemberSyncEvent
			if err := json.Unmarshal(msg.Value, &evt); err != nil {
				return err
			}
			applyGroupMemberSyncEvent(evt.GroupID, evt.Op, evt.Version, evt.MemberUserIDs)
			return nil
		})
		if err != nil {
			log.Printf("gateway group-member-sync consumer stopped: %v", err)
		}
	}()
}
