package collab

import (
	"strings"
	"testing"
)

// TestValidateAgentIDRejectsRedisKeyInjection is the important one: agent IDs
// are interpolated into Redis key names of the form
// collab:session:<uuid>:agent:<agentID>. An ID containing ':' could collide
// with a sibling key such as …:events or …:agents.
func TestValidateAgentIDRejectsRedisKeyInjection(t *testing.T) {
	ids := []struct {
		name    string
		agentID string
	}{
		{"colon separator", "agent:events"},
		{"colon prefix", ":agents"},
		{"key namespace collision", "a:agents"},
		{"full key path", "collab:session:x:agent:y"},
		{"glob wildcard", "agent*"},
		{"glob question mark", "agent?"},
		{"glob bracket", "agent[a-z]"},
		{"newline", "agent\nSET evil 1"},
		{"carriage return", "agent\r\nFLUSHALL"},
		{"space", "agent name"},
		{"NUL byte", "agent\x00"},
		{"leading hyphen", "-agent"},
		{"leading dot", ".agent"},
		{"empty", ""},
		{"too long", strings.Repeat("a", MaxAgentIDLength+1)},
		{"slash", "agent/other"},
		{"curly braces", "agent{shard}"},
	}

	for _, tc := range ids {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateAgentID(tc.agentID); err == nil {
				t.Errorf("ValidateAgentID(%q) = nil, want error", tc.agentID)
			}
		})
	}
}

func TestValidateAgentIDAcceptsLegitimateIDs(t *testing.T) {
	ids := []string{
		"agent_a",
		"agent-b",
		"agent.c",
		"architect",
		"Developer1",
		"a",
		"agent_2026_07_31",
		strings.Repeat("a", MaxAgentIDLength), // exact maximum
	}

	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			if err := ValidateAgentID(id); err != nil {
				t.Errorf("ValidateAgentID(%q) = %v, want nil", id, err)
			}
		})
	}
}

// TestValidateMessageBodyBoundsSize: an unbounded body lets one agent fill the
// message table and saturate every peer's send buffer.
func TestValidateMessageBodyBoundsSize(t *testing.T) {
	t.Run("empty rejected", func(t *testing.T) {
		if err := ValidateMessageBody(""); err == nil {
			t.Error("ValidateMessageBody(\"\") = nil, want error")
		}
	})

	t.Run("at maximum accepted", func(t *testing.T) {
		body := strings.Repeat("x", MaxMessageBytes)
		if err := ValidateMessageBody(body); err != nil {
			t.Errorf("ValidateMessageBody(%d bytes) = %v, want nil", len(body), err)
		}
	})

	t.Run("one over maximum rejected", func(t *testing.T) {
		body := strings.Repeat("x", MaxMessageBytes+1)
		if err := ValidateMessageBody(body); err == nil {
			t.Errorf("ValidateMessageBody(%d bytes) = nil, want error", len(body))
		}
	})

	t.Run("invalid UTF-8 rejected", func(t *testing.T) {
		if err := ValidateMessageBody("\xff\xfe invalid"); err == nil {
			t.Error("ValidateMessageBody(invalid UTF-8) = nil, want error")
		}
	})

	t.Run("multibyte UTF-8 accepted", func(t *testing.T) {
		if err := ValidateMessageBody("héllo wörld 日本語 🎉"); err != nil {
			t.Errorf("ValidateMessageBody(multibyte) = %v, want nil", err)
		}
	})
}

func TestValidateMessageTypeRejectsUnknown(t *testing.T) {
	invalid := []string{"", "unknown", "DIRECT", "system_admin", "broadcast ", "../direct"}

	for _, msgType := range invalid {
		t.Run(msgType, func(t *testing.T) {
			if err := ValidateMessageType(msgType); err == nil {
				t.Errorf("ValidateMessageType(%q) = nil, want error", msgType)
			}
		})
	}

	for _, msgType := range []string{MessageTypeDirect, MessageTypeBroadcast, MessageTypeSystem} {
		t.Run("valid/"+msgType, func(t *testing.T) {
			if err := ValidateMessageType(msgType); err != nil {
				t.Errorf("ValidateMessageType(%q) = %v, want nil", msgType, err)
			}
		})
	}
}

func TestValidateAgentStatusRejectsUnknown(t *testing.T) {
	for _, status := range []string{"", "ACTIVE", "online", "busy"} {
		t.Run(status, func(t *testing.T) {
			if err := ValidateAgentStatus(status); err == nil {
				t.Errorf("ValidateAgentStatus(%q) = nil, want error", status)
			}
		})
	}

	for _, status := range []string{AgentStatusActive, AgentStatusIdle, AgentStatusDisconnected} {
		t.Run("valid/"+status, func(t *testing.T) {
			if err := ValidateAgentStatus(status); err != nil {
				t.Errorf("ValidateAgentStatus(%q) = %v, want nil", status, err)
			}
		})
	}
}

// TestValidateMaxAgentsBoundsResourceUse: each agent holds a WebSocket
// connection plus a read and write goroutine, so the cap must itself be capped.
func TestValidateMaxAgentsBoundsResourceUse(t *testing.T) {
	cases := []struct {
		maxAgents int
		wantErr   bool
	}{
		{-1, true},
		{0, true},
		{1, false},
		{4, false},
		{MaxAgentsPerSession, false},
		{MaxAgentsPerSession + 1, true},
		{1_000_000, true},
	}

	for _, tc := range cases {
		t.Run(strings.TrimSpace(strings.Repeat(" ", 0)+itoa(tc.maxAgents)), func(t *testing.T) {
			err := ValidateMaxAgents(tc.maxAgents)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateMaxAgents(%d) = nil, want error", tc.maxAgents)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateMaxAgents(%d) = %v, want nil", tc.maxAgents, err)
			}
		})
	}
}

func TestValidateFilePathBoundsLength(t *testing.T) {
	t.Run("empty accepted", func(t *testing.T) {
		// An agent that is not editing anything reports an empty path.
		if err := ValidateFilePath(""); err != nil {
			t.Errorf("ValidateFilePath(\"\") = %v, want nil", err)
		}
	})

	t.Run("at maximum accepted", func(t *testing.T) {
		if err := ValidateFilePath(strings.Repeat("a", MaxFilePathLength)); err != nil {
			t.Errorf("ValidateFilePath(max) = %v, want nil", err)
		}
	})

	t.Run("over maximum rejected", func(t *testing.T) {
		if err := ValidateFilePath(strings.Repeat("a", MaxFilePathLength+1)); err == nil {
			t.Error("ValidateFilePath(max+1) = nil, want error")
		}
	})

	t.Run("invalid UTF-8 rejected", func(t *testing.T) {
		if err := ValidateFilePath("\xff\xfe.go"); err == nil {
			t.Error("ValidateFilePath(invalid UTF-8) = nil, want error")
		}
	})
}

func TestValidateAgentNameBoundsLength(t *testing.T) {
	if err := ValidateAgentName(""); err == nil {
		t.Error("ValidateAgentName(\"\") = nil, want error")
	}
	if err := ValidateAgentName(strings.Repeat("a", MaxAgentNameLength+1)); err == nil {
		t.Error("ValidateAgentName(too long) = nil, want error")
	}
	// Display names may contain anything printable.
	if err := ValidateAgentName("Architect (backend) — 日本語"); err != nil {
		t.Errorf("ValidateAgentName(unicode) = %v, want nil", err)
	}
}

// itoa avoids importing strconv just for subtest names.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

func TestValidateGraphRelation(t *testing.T) {
	for _, ok := range []string{RelationReportsTo, RelationReviews, RelationHandoff, RelationPeer} {
		if err := ValidateGraphRelation(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	if err := ValidateGraphRelation("owns"); err == nil {
		t.Fatal("expected error for unknown relation")
	}
}
