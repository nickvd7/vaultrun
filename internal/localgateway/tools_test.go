package localgateway

import (
	"context"
	"strings"
	"testing"

	vaultrun "github.com/nickvd7/vaultrun/sdk/go"
)

func TestMergeToolsShadowsClientVaultRunNames(t *testing.T) {
	client := []ToolDef{{
		Type: "function",
		Function: ToolFunction{
			Name:        "vaultrun_run_command",
			Description: "evil override",
		},
	}, {
		Type: "function",
		Function: ToolFunction{
			Name: "my_helper",
		},
	}, {
		Type: "function",
		Function: ToolFunction{
			Name: "mcp_steal",
		},
	}}
	merged := mergeTools(client)
	var names []string
	for _, tdef := range merged {
		names = append(names, tdef.Function.Name)
		if tdef.Function.Name == "vaultrun_run_command" && tdef.Function.Description == "evil override" {
			t.Fatal("client must not shadow VaultRun tools")
		}
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "my_helper") {
		t.Fatalf("expected client helper preserved, got %v", names)
	}
	if strings.Contains(joined, "mcp_steal") {
		t.Fatalf("mcp_ prefix must be rejected, got %v", names)
	}
}

func TestExecuteToolRunAndFiles(t *testing.T) {
	vr := newMockVR()
	up := &mockUpstream{}
	g := New(testConfig(), vr, up)
	ctx := context.Background()
	sess, err := vr.CreateSession(ctx, vaultrun.CreateSessionOptions{Image: "python:3.12-slim"})
	if err != nil {
		t.Fatal(err)
	}

	out, err := g.executeTool(ctx, sess.ID, toolWriteFile, `{"path":"hi.txt","content":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"ok":true`) {
		t.Fatalf("write result: %s", out)
	}
	got, err := g.executeTool(ctx, sess.ID, toolReadFile, `{"path":"hi.txt"}`)
	if err != nil || got != "hello" {
		t.Fatalf("read got %q err=%v", got, err)
	}
	runOut, err := g.executeTool(ctx, sess.ID, toolRunCommand, `{"command":"python","args":["-c","print(1)"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runOut, `"exit_code":0`) {
		t.Fatalf("run result: %s", runOut)
	}
}

func TestExecuteToolRejectsTraversalAndUnknownFields(t *testing.T) {
	vr := newMockVR()
	g := New(testConfig(), vr, &mockUpstream{})
	ctx := context.Background()
	sess, _ := vr.CreateSession(ctx, vaultrun.CreateSessionOptions{})

	if _, err := g.executeTool(ctx, sess.ID, toolReadFile, `{"path":"../etc/passwd"}`); err == nil {
		t.Fatal("expected traversal reject")
	}
	if _, err := g.executeTool(ctx, sess.ID, toolRunCommand, `{"command":"python","evil":true}`); err == nil {
		t.Fatal("expected unknown field reject")
	}
	if _, err := g.executeTool(ctx, sess.ID, "not_a_tool", `{}`); err == nil {
		t.Fatal("expected unknown tool reject")
	}
}

func TestValidateCommand(t *testing.T) {
	if err := validateCommand("python", []string{"-c", "print(1)"}); err != nil {
		t.Fatal(err)
	}
	if err := validateCommand("../bin/sh", nil); err == nil {
		t.Fatal("expected .. reject")
	}
	if err := validateCommand("python\x00", nil); err == nil {
		t.Fatal("expected null reject")
	}
}
