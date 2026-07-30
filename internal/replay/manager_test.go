package replay

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nickvd7/vaultrun/internal/models"
)

func TestSignCheckpoint(t *testing.T) {
	mgr := &Manager{signingKey: []byte("test-key-12345678901234567890")}
	
	cp := &Checkpoint{
		SessionID:           uuid.New(),
		WorkspaceSnapshotID: uuid.New(),
		CheckpointNumber:    1,
		CreatedAt:           time.Now(),
	}
	
	sig1 := mgr.signCheckpoint(cp)
	sig2 := mgr.signCheckpoint(cp)
	
	// Same checkpoint should produce same signature
	if sig1 != sig2 {
		t.Errorf("signatures don't match: %s != %s", sig1, sig2)
	}
	
	// Signature should be hex-encoded HMAC
	if len(sig1) != 64 { // SHA-256 HMAC = 32 bytes = 64 hex chars
		t.Errorf("unexpected signature length: got %d, want 64", len(sig1))
	}
	
	// Verify it's valid hex
	_, err := hex.DecodeString(sig1)
	if err != nil {
		t.Errorf("signature is not valid hex: %v", err)
	}
}

func TestVerifyCheckpoint(t *testing.T) {
	mgr := &Manager{signingKey: []byte("test-key-12345678901234567890")}
	
	cp := &Checkpoint{
		SessionID:           uuid.New(),
		WorkspaceSnapshotID: uuid.New(),
		CheckpointNumber:    1,
		CreatedAt:           time.Now(),
	}
	
	// Sign checkpoint
	cp.Signature = mgr.signCheckpoint(cp)
	
	// Valid signature should verify
	if !mgr.verifyCheckpoint(cp) {
		t.Error("valid checkpoint failed verification")
	}
	
	// Tampered checkpoint should fail
	cp.CheckpointNumber = 2
	if mgr.verifyCheckpoint(cp) {
		t.Error("tampered checkpoint passed verification")
	}
}

func TestRedactEnvVars(t *testing.T) {
	mgr := &Manager{}
	
	tests := []struct {
		name     string
		input    map[string]string
		expected map[string]interface{}
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: map[string]interface{}{},
		},
		{
			name: "no sensitive vars",
			input: map[string]string{
				"PATH":     "/usr/bin",
				"USER":     "ubuntu",
				"HOME":     "/home/ubuntu",
			},
			expected: map[string]interface{}{
				"PATH":     "/usr/bin",
				"USER":     "ubuntu",
				"HOME":     "/home/ubuntu",
			},
		},
		{
			name: "sensitive vars redacted",
			input: map[string]string{
				"PATH":                  "/usr/bin",
				"AWS_ACCESS_KEY_ID":     "AKIA1234567890",
				"AWS_SECRET_ACCESS_KEY": "secret123",
				"DATABASE_PASSWORD":     "pass123",
				"API_KEY":               "key123",
			},
			expected: map[string]interface{}{
				"PATH":                  "/usr/bin",
				"AWS_ACCESS_KEY_ID":     "AKIA1234567890", // Not in sensitive list
				"AWS_SECRET_ACCESS_KEY": "[REDACTED]",
				"DATABASE_PASSWORD":     "[REDACTED]",
				"API_KEY":               "[REDACTED]",
			},
		},
		{
			name: "case insensitive matching",
			input: map[string]string{
				"secret_token": "token123",
				"SECRET_KEY":   "key123",
				"My_Password":  "pass123",
			},
			expected: map[string]interface{}{
				"secret_token": "[REDACTED]",
				"SECRET_KEY":   "[REDACTED]",
				"My_Password":  "[REDACTED]",
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mgr.redactEnvVars(tt.input)
			
			if len(result) != len(tt.expected) {
				t.Errorf("length mismatch: got %d, want %d", len(result), len(tt.expected))
			}
			
			for k, expectedV := range tt.expected {
				gotV, ok := result[k]
				if !ok {
					t.Errorf("missing key: %s", k)
					continue
				}
				if gotV != expectedV {
					t.Errorf("key %s: got %v, want %v", k, gotV, expectedV)
				}
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello"},
		{"", 10, ""},
		{"x", 0, ""},
	}
	
	for _, tt := range tests {
		result := truncate(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}

func TestArgsToJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected models.JSONB
	}{
		{
			name:     "nil args",
			input:    nil,
			expected: models.JSONB{},
		},
		{
			name:     "empty args",
			input:    []string{},
			expected: models.JSONB{},
		},
		{
			name:  "single arg",
			input: []string{"hello"},
			expected: models.JSONB{
				"0": "hello",
			},
		},
		{
			name:  "multiple args",
			input: []string{"python", "script.py", "--verbose"},
			expected: models.JSONB{
				"0": "python",
				"1": "script.py",
				"2": "--verbose",
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := argsToJSON(tt.input)
			
			if len(result) != len(tt.expected) {
				t.Errorf("length mismatch: got %d, want %d", len(result), len(tt.expected))
			}
			
			for k, expectedV := range tt.expected {
				gotV, ok := result[k]
				if !ok {
					t.Errorf("missing key: %s", k)
					continue
				}
				if gotV != expectedV {
					t.Errorf("key %s: got %v, want %v", k, gotV, expectedV)
				}
			}
		})
	}
}

// Benchmark tests

func BenchmarkSignCheckpoint(b *testing.B) {
	mgr := &Manager{signingKey: []byte("test-key-12345678901234567890")}
	cp := &Checkpoint{
		SessionID:           uuid.New(),
		WorkspaceSnapshotID: uuid.New(),
		CheckpointNumber:    1,
		CreatedAt:           time.Now(),
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.signCheckpoint(cp)
	}
}

func BenchmarkVerifyCheckpoint(b *testing.B) {
	mgr := &Manager{signingKey: []byte("test-key-12345678901234567890")}
	cp := &Checkpoint{
		SessionID:           uuid.New(),
		WorkspaceSnapshotID: uuid.New(),
		CheckpointNumber:    1,
		CreatedAt:           time.Now(),
	}
	cp.Signature = mgr.signCheckpoint(cp)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.verifyCheckpoint(cp)
	}
}

func BenchmarkRedactEnvVars(b *testing.B) {
	mgr := &Manager{}
	env := map[string]string{
		"PATH":                  "/usr/bin",
		"AWS_ACCESS_KEY_ID":     "AKIA1234567890",
		"AWS_SECRET_ACCESS_KEY": "secret123",
		"DATABASE_PASSWORD":     "pass123",
		"API_KEY":               "key123",
		"USER":                  "ubuntu",
		"HOME":                  "/home/ubuntu",
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.redactEnvVars(env)
	}
}
