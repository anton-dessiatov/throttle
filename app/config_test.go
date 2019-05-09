package app

import (
	"encoding/json"
	"testing"
)

func TestUnmarshalLimit(t *testing.T) {
	var limit Limit
	err := json.Unmarshal([]byte("1"), &limit)
	if err != nil || limit != Limit(1) {
		t.Errorf("Failed to unmarshal '1': %v %v", err, limit)
	}
	err = json.Unmarshal([]byte("\"1\""), &limit)
	if err != nil || limit != Limit(1) {
		t.Errorf("Failed to unmarshal '1': %v %v", err, limit)
	}
	err = json.Unmarshal([]byte("\"8bps\""), &limit)
	if err != nil || limit != Limit(1) {
		t.Errorf("Failed to unmarshal '8bps': %v %v", err, limit)
	}
}
