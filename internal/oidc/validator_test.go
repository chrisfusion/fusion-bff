package oidc

import (
	"encoding/json"
	"testing"
)

func TestGroupClaimUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"plain string", `"platform-admin"`, "platform-admin"},
		{"plain string with path", `"/team-data"`, "/team-data"},
		{"object with name", `{"id":"abc","name":"platform-admin","path":"/platform-admin"}`, "platform-admin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var g groupClaim
			if err := json.Unmarshal([]byte(tt.json), &g); err != nil {
				t.Fatalf("UnmarshalJSON() error = %v", err)
			}
			if string(g) != tt.want {
				t.Errorf("UnmarshalJSON() = %q, want %q", string(g), tt.want)
			}
		})
	}
}

func TestGroupsClaimUnmarshalJSON_MixedArray(t *testing.T) {
	var raw struct {
		Groups []groupClaim `json:"groups"`
	}
	data := []byte(`{"groups":[{"name":"test"},"other-group","/team-data"]}`)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	want := []string{"test", "other-group", "/team-data"}
	if len(raw.Groups) != len(want) {
		t.Fatalf("got %d groups, want %d", len(raw.Groups), len(want))
	}
	for i, g := range raw.Groups {
		if string(g) != want[i] {
			t.Errorf("groups[%d] = %q, want %q", i, string(g), want[i])
		}
	}
}

func TestExtractUserID(t *testing.T) {
	claims := map[string]json.RawMessage{
		"sub":     json.RawMessage(`"abc-123"`),
		"uid":     json.RawMessage(`"corp-42"`),
		"numeric": json.RawMessage(`4711`),
		"obj":     json.RawMessage(`{"a":1}`),
	}
	tests := []struct {
		name, claim, want string
	}{
		{"default is sub", "", "abc-123"},
		{"explicit sub", "sub", "abc-123"},
		{"custom claim", "uid", "corp-42"},
		{"numeric claim", "numeric", "4711"},
		{"missing claim", "nope", ""},
		{"non-scalar claim", "obj", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractUserID(claims, tt.claim); got != tt.want {
				t.Errorf("ExtractUserID(%q) = %q, want %q", tt.claim, got, tt.want)
			}
		})
	}
}
