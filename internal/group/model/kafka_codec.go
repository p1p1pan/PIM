package model

import (
	"encoding/json"
	"errors"
	"sync/atomic"

	"pim/internal/config"
	pbgroup "pim/proto/group/v1"

	"google.golang.org/protobuf/proto"
)

const (
	GroupKafkaDecodePB   = "pb"
	GroupKafkaDecodeJSON = "json"
)

var groupKafkaJSONFallbackCount atomic.Uint64

func EncodeGroupKafkaMessagePB(km GroupKafkaMessage) ([]byte, error) {
	msg := &pbgroup.GroupKafkaMessage{
		TraceId:    km.TraceID,
		EventId:    km.EventID,
		GroupId:    uint64(km.GroupID),
		FromUserId: uint64(km.From),
		Content:    km.Content,
	}
	return proto.Marshal(msg)
}

// DecodeGroupKafkaMessage supports protobuf first, then JSON for backward compatibility.
func DecodeGroupKafkaMessage(data []byte) (GroupKafkaMessage, error) {
	km, _, err := DecodeGroupKafkaMessageWithMode(data)
	return km, err
}

func DecodeGroupKafkaMessageWithMode(data []byte) (GroupKafkaMessage, string, error) {
	var out GroupKafkaMessage
	var pbMsg pbgroup.GroupKafkaMessage
	if err := proto.Unmarshal(data, &pbMsg); err == nil {
		if pbMsg.GetGroupId() > 0 && pbMsg.GetFromUserId() > 0 {
			out.TraceID = pbMsg.GetTraceId()
			out.EventID = pbMsg.GetEventId()
			out.GroupID = uint(pbMsg.GetGroupId())
			out.From = uint(pbMsg.GetFromUserId())
			out.Content = pbMsg.GetContent()
			return out, GroupKafkaDecodePB, nil
		}
	}

	if !config.KafkaGroupMessageJSONFallbackEnabled {
		return GroupKafkaMessage{}, "", errors.New("group kafka pb decode failed and json fallback disabled")
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return GroupKafkaMessage{}, "", err
	}
	if out.GroupID == 0 || out.From == 0 {
		return GroupKafkaMessage{}, "", errors.New("invalid group kafka payload")
	}
	groupKafkaJSONFallbackCount.Add(1)
	return out, GroupKafkaDecodeJSON, nil
}

func GroupKafkaJSONFallbackCount() uint64 {
	return groupKafkaJSONFallbackCount.Load()
}
