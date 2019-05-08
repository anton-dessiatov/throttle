package app

import (
	"encoding/json"
	"testing"
)

func TestUnmarshalLimit(t *testing.T) {
	var limit Limit
	err := json.Unmarshal([]byte("1"), &limit)
	if err != nil || limit != Limit(1) {
		t.Error("Failed to unmarshal '1'")
	}
	// err = json.Unmarshal([]byte("1bps"), &limit)
	// if err != nil || limit != Limit
}
