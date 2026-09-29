package rbac

import (
	"context"
	"slices"
	"testing"
)

func TestResolveOwnerGroups_DefaultGroupWithoutDB(t *testing.T) {
	e := NewEngine(&RBACConfig{}, nil).WithDefaultOwnerGroup("everyone")
	got, err := e.ResolveOwnerGroups(context.Background(), "u1", "a@b.c", []string{"team-x"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"everyone"}) {
		t.Errorf("got %v, want [everyone]", got)
	}
}

func TestResolveOwnerGroups_DisabledDefault(t *testing.T) {
	e := NewEngine(&RBACConfig{}, nil)
	got, err := e.ResolveOwnerGroups(context.Background(), "u1", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %#v, want empty non-nil slice", got)
	}
}

func TestEffectivePreferredOwnerGroup(t *testing.T) {
	e := NewEngine(&RBACConfig{}, nil).WithDefaultOwnerGroup("everyone")
	groups := []string{"everyone", "team-x"}
	tests := []struct {
		name, stored string
		want         string
	}{
		{"stored and member", "team-x", "team-x"},
		{"no preference", "", "everyone"},
		{"membership lost", "team-y", "everyone"},
	}
	for _, tt := range tests {
		if got := e.EffectivePreferredOwnerGroup(tt.stored, groups); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
	if got := NewEngine(&RBACConfig{}, nil).EffectivePreferredOwnerGroup("", nil); got != "" {
		t.Errorf("disabled default: got %q, want empty", got)
	}
}
