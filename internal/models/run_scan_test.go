package models

import (
	"reflect"
	"testing"
)

// Regression: migration 011 added checkpoint columns on runs. Without matching
// db tags, sqlx SELECT * fails and /run returns a stale pending stub while
// list_runs responds 500 (CI e2e smoke).
func TestRunHasReplayColumns(t *testing.T) {
	required := []string{"checkpoint_id", "restored_from_checkpoint_id"}
	typ := reflect.TypeOf(Run{})
	tags := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		if tag := typ.Field(i).Tag.Get("db"); tag != "" && tag != "-" {
			tags[tag] = true
		}
	}
	for _, name := range required {
		if !tags[name] {
			t.Errorf("models.Run missing db:%q (required by migration 011)", name)
		}
	}
}
