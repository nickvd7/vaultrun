package secrets

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestRedactorNilIsNoop(t *testing.T) {
	var r *Redactor
	if got := r.Redact("hello"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	var buf bytes.Buffer
	w := r.Writer(&buf)
	_, _ = w.Write([]byte("x"))
	_ = w.Close()
	if buf.String() != "x" {
		t.Fatalf("got %q", buf.String())
	}
	if NewRedactor(nil) != nil || NewRedactor(map[string]string{"A": "abc"}) != nil {
		t.Fatal("expected nil redactor for empty or too-short secrets")
	}
}

func TestRedactReplacesValueAndBase64(t *testing.T) {
	r := NewRedactor(map[string]string{"DB_PASSWORD": "hunter2!"})
	b64 := base64.StdEncoding.EncodeToString([]byte("hunter2!"))
	got := r.Redact("pw=hunter2! enc=" + b64 + " ok")
	want := "pw=[REDACTED:DB_PASSWORD] enc=[REDACTED:DB_PASSWORD] ok"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRedactLongestWins(t *testing.T) {
	r := NewRedactor(map[string]string{"SHORT": "abcd", "LONG": "abcdefgh"})
	if got := r.Redact("x abcdefgh y"); got != "x [REDACTED:LONG] y" {
		t.Fatalf("got %q", got)
	}
}

func TestRedactLeavesOtherOutputAlone(t *testing.T) {
	r := NewRedactor(map[string]string{"K": "s3cr3t-value"})
	in := "line one\nline two with s3cr3t but not the whole value\n"
	if got := r.Redact(in); got != in {
		t.Fatalf("got %q", got)
	}
}

func TestWriterRedactsAcrossChunks(t *testing.T) {
	r := NewRedactor(map[string]string{"TOKEN": "abcdefghij"})
	for split := 1; split < len("abcdefghij"); split++ {
		var buf bytes.Buffer
		w := r.Writer(&buf)
		in := "a:abcdefghij:z"
		idx := strings.Index(in, "abcdefghij") + split
		_, _ = w.Write([]byte(in[:idx]))
		_, _ = w.Write([]byte(in[idx:]))
		_ = w.Close()
		if got := buf.String(); got != "a:[REDACTED:TOKEN]:z" {
			t.Fatalf("split %d: got %q", split, got)
		}
	}
}

func TestWriterFlushesPartialPrefixOnClose(t *testing.T) {
	r := NewRedactor(map[string]string{"TOKEN": "abcdefghij"})
	var buf bytes.Buffer
	w := r.Writer(&buf)
	_, _ = w.Write([]byte("end abcde"))
	if buf.String() != "end " {
		t.Fatalf("held-back tail should not be written yet, got %q", buf.String())
	}
	_ = w.Close()
	if buf.String() != "end abcde" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestWriterByteAtATime(t *testing.T) {
	r := NewRedactor(map[string]string{"TOKEN": "abcdefghij"})
	var buf bytes.Buffer
	w := r.Writer(&buf)
	for _, b := range []byte("x abcdefghij abcdefgh y") {
		_, _ = w.Write([]byte{b})
	}
	_ = w.Close()
	if got := buf.String(); got != "x [REDACTED:TOKEN] abcdefgh y" {
		t.Fatalf("got %q", got)
	}
}
