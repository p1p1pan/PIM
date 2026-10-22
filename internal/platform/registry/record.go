package registry

import "encoding/json"

// EndpointRecord 注册到 etcd 的实例元数据（JSON）。
type EndpointRecord struct {
	Addr   string `json:"addr"`
	NodeID string `json:"node_id,omitempty"`
}

// DecodeEndpointRecord 解析 etcd value。
func DecodeEndpointRecord(data []byte) (EndpointRecord, error) {
	var r EndpointRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return EndpointRecord{}, err
	}
	return r, nil
}

func encodeRecord(r EndpointRecord) ([]byte, error) {
	return json.Marshal(r)
}
