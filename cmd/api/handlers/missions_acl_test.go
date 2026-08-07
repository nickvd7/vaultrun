package handlers

import (
	"testing"

	"github.com/nickvd7/vaultrun/internal/missions"
)

// Unit-level ACL expectations for missions (no HTTP/DB). Published definition
// readability must not imply runs/cost visibility.
func TestMissionACLSemantics(t *testing.T) {
	h := &MissionHandler{}
	pub := &missions.Mission{Published: true, CreatedBy: "alice"}
	draft := &missions.Mission{Published: false, CreatedBy: "alice"}

	if !h.mayRead(nil, "stranger", pub) {
		t.Fatal("stranger should read published definition")
	}
	if h.mayReadRuns(nil, "stranger", pub) {
		t.Fatal("stranger must not read published mission runs")
	}
	if !h.mayReadRuns(nil, "alice", pub) {
		t.Fatal("creator should read runs")
	}
	if !h.mayWrite(nil, "alice", pub) {
		t.Fatal("creator should write")
	}
	if h.mayWrite(nil, "stranger", pub) {
		t.Fatal("stranger must not write published mission")
	}
	if h.mayRead(nil, "stranger", draft) {
		t.Fatal("stranger must not read draft")
	}
	if !h.mayRead(nil, "master", draft) || !h.mayReadRuns(nil, "master", draft) {
		t.Fatal("master bypass")
	}
}
