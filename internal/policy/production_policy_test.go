package policy

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
)

func TestProductionPolicyAllowsAgentsAndBlocksNamespaceTools(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "policy", "production.rego")
	hook, err := NewOPAHookFromFile(context.Background(), path)
	if err != nil {
		t.Fatalf("compile production policy: %v", err)
	}
	sid := uuid.New()
	if d := hook.EvalCommand(context.Background(), sid, "python", []string{"escape.py"}); !d.Allowed {
		t.Fatalf("python should be allowed, got %s", d.Reason)
	}
	if d := hook.EvalCommand(context.Background(), sid, "mount", nil); d.Allowed {
		t.Fatal("mount must be denied")
	}
	if d := hook.EvalFileAccess(context.Background(), sid, "/workspace/out.txt", true); !d.Allowed {
		t.Fatalf("write under /workspace should be allowed, got %s", d.Reason)
	}
	if d := hook.EvalFileAccess(context.Background(), sid, "/etc/passwd", true); d.Allowed {
		t.Fatal("write outside the workspace must be denied")
	}
	if d := hook.EvalFileAccess(context.Background(), sid, "/etc/passwd", false); !d.Allowed {
		t.Fatal("reads should stay allowed")
	}
}
