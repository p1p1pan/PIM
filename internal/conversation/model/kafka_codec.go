package model

import (
	"encoding/json"
	"errors"

	pbconversation "pim/internal/conversation/pb"

	"google.golang.org/protobuf/proto"
)

func EncodeKafkaMessagePB(km KafkaMessage) ([]byte, error) {
	msg := &pbconversation.IMKafkaMessage{
		TraceId:     km.TraceID,
		FromUserId:  uint64(km.FromUserID),
		ToUserId:    uint64(km.ToUserID),
		Content:     km.Content,
		ClientMsgId: km.ClientMsgID,
	}
	return proto.Marshal(msg)
}

// DecodeKafkaMessage supports protobuf first, then JSON for backward compatibility.
func DecodeKafkaMessage(data []byte) (KafkaMessage, error) {
	var out KafkaMessage
	var pbMsg pbconversation.IMKafkaMessage
	if err := proto.Unmarshal(data, &pbMsg); err == nil {
		// guard: protobuf decode should contain core IDs for this event type
		if pbMsg.FromUserId > 0 && pbMsg.ToUserId > 0 {
			out.TraceID = pbMsg.GetTraceId()
			out.FromUserID = uint(pbMsg.GetFromUserId())
			out.ToUserID = uint(pbMsg.GetToUserId())
			out.Content = pbMsg.GetContent()
			out.ClientMsgID = pbMsg.GetClientMsgId()
			return out, nil
		}
	}

	if err := json.Unmarshal(data, &out); err != nil {
		return KafkaMessage{}, err
	}
	if out.FromUserID == 0 || out.ToUserID == 0 {
		return KafkaMessage{}, errors.New("invalid kafka message payload")
	}
	return out, nil
}
