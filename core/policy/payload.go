package policy

import (
	"encoding/json"
	"fmt"
)

// payloadVersion1 is the only recognized approval-request payload version in v1.
const payloadVersion1 = "1"

// EscalationPayload is the versioned approval-request payload carried in
// TaskStatus.Message during INPUT_REQUIRED.
type EscalationPayload struct {
	Version    string `json:"version"`
	ActionKind string `json:"action_kind"`
	TaskID     string `json:"task_id"`
}

// MarshalPayload serializes an EscalationPayload to JSON bytes.
// It always stamps Version = payloadVersion1.
func MarshalPayload(p EscalationPayload) ([]byte, error) {
	p.Version = payloadVersion1
	return json.Marshal(p)
}

// ValidatePayload deserializes data and validates the version field.
// Returns an error if the version is unrecognized or the data is malformed.
func ValidatePayload(data []byte) (*EscalationPayload, error) {
	var p EscalationPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("policy: payload decode: %w", err)
	}
	if p.Version != payloadVersion1 {
		return nil, fmt.Errorf("policy: unrecognized payload version %q", p.Version)
	}
	return &p, nil
}
