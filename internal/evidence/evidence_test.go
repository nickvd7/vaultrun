package evidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSigner(t *testing.T) *Signer {
	t.Helper()
	seed, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSigningKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testPack() *Pack {
	exit := 0
	return &Pack{
		Schema:     PackSchema,
		ID:         "pack-1",
		CreatedAt:  time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC),
		Issuer:     Issuer{Product: "vaultrun", Version: "test"},
		ExportedBy: "master",
		Session:    Session{ID: "s1", Image: "python:3.12-slim", Runtime: "runsc", Status: "running"},
		Runs:       []Run{NewRun(Run{ID: "r1", Command: "python", Args: []string{"x.py"}, ExitCode: &exit}, "hello <world> & é\n", "")},
		Files:      []File{{Path: "/x.py", Size: 3, SHA256: SHA256Hex("abc")}},
		Audit:      AuditRecord{Scope: AuditScopeAll, Signed: true, Entries: []AuditEntry{{ID: "a1", Action: "session.created", SigStatus: "valid"}}},
	}
}

func export(t *testing.T, s *Signer, p *Pack) []byte {
	t.Helper()
	env, err := s.Sign(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSignVerifyRoundTrip(t *testing.T) {
	s := testSigner(t)
	got, err := Verify(export(t, s, testPack()), s.PublicKey())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Session.Runtime != "runsc" || got.Runs[0].Stdout != "hello <world> & é\n" {
		t.Errorf("pack did not round-trip: %+v", got)
	}
	if f := Check(got); len(f) != 0 {
		t.Errorf("clean pack has findings: %+v", f)
	}
}

// Reformatting the file (jq ., an editor) must not break the signature: the
// signed bytes are carried as base64, not as re-encodable JSON.
func TestVerifySurvivesReformatting(t *testing.T) {
	s := testSigner(t)
	var buf bytes.Buffer
	if err := json.Indent(&buf, export(t, s, testPack()), "", "    "); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(buf.Bytes(), s.PublicKey()); err != nil {
		t.Fatalf("reformatted pack rejected: %v", err)
	}
}

// Every single-byte change to the signed pack is detected.
func TestVerifyDetectsTampering(t *testing.T) {
	s := testSigner(t)
	env, err := s.Sign(testPack())
	if err != nil {
		t.Fatal(err)
	}
	for i := range env.Pack {
		changed := *env
		changed.Pack = bytes.Clone(env.Pack)
		changed.Pack[i] ^= 0x01
		b, _ := json.Marshal(changed)
		if _, err := Verify(b, s.PublicKey()); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("byte %d flipped: want ErrBadSignature, got %v", i, err)
		}
	}

	// Editing a decoded field and re-encoding is a change too.
	var p Pack
	_ = json.Unmarshal(env.Pack, &p)
	p.Runs[0].Stdout = "something else"
	edited := *env
	edited.Pack, _ = json.Marshal(p)
	b, _ := json.Marshal(edited)
	if _, err := Verify(b, s.PublicKey()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("edited stdout: want ErrBadSignature, got %v", err)
	}
}

func TestVerifyRejectsOtherKeyAndBadInput(t *testing.T) {
	s, other := testSigner(t), testSigner(t)
	b := export(t, s, testPack())
	_, err := Verify(b, other.PublicKey())
	if !errors.Is(err, ErrBadSignature) || !strings.Contains(err.Error(), s.KeyID()) {
		t.Errorf("other key: want ErrBadSignature naming %s, got %v", s.KeyID(), err)
	}

	// A forged key_id does not help: the signature itself is checked.
	env, _ := s.Sign(testPack())
	env.Signature.KeyID = other.KeyID()
	forged, _ := json.Marshal(env)
	if _, err := Verify(forged, other.PublicKey()); !errors.Is(err, ErrBadSignature) {
		t.Errorf("forged key_id: want ErrBadSignature, got %v", err)
	}

	for name, in := range map[string]string{
		"not json":  "nope",
		"empty":     `{}`,
		"other alg": `{"pack":"e30=","signature":{"alg":"hmac-sha256"}}`,
	} {
		if _, err := Verify([]byte(in), s.PublicKey()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseSigningKey(t *testing.T) {
	seed, pubPEM, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSigningKey("  " + seed + "\n")
	if err != nil {
		t.Fatalf("hex seed: %v", err)
	}
	pub, err := ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Equal(s.PublicKey()) || !bytes.Equal(s.PublicKeyPEM(), pubPEM) {
		t.Error("GenerateKey's public key does not match the seed")
	}

	// PKCS#8 PEM file, as openssl genpkey -algorithm ed25519 writes it.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := ParseSigningKey(path)
	if err != nil {
		t.Fatalf("PEM file: %v", err)
	}
	if fromFile.KeyID() != KeyID(priv.Public().(ed25519.PublicKey)) {
		t.Error("PEM file key id mismatch")
	}

	for name, in := range map[string]string{
		"empty":       "",
		"short hex":   "abcd",
		"missing":     filepath.Join(t.TempDir(), "nope.pem"),
		"public only": writeTemp(t, pubPEM),
	} {
		if _, err := ParseSigningKey(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParsePublicKeyPEM([]byte("garbage")); err == nil {
		t.Error("garbage public key accepted")
	}
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewRunCapsOutputAndHashesAll(t *testing.T) {
	long := strings.Repeat("a", MaxOutputBytes-1) + "éé" // a 2-byte rune straddles the cap
	r := NewRun(Run{ID: "r"}, long, "err")
	if !r.StdoutTruncated || len(r.Stdout) > MaxOutputBytes || !strings.HasPrefix(long, r.Stdout) {
		t.Errorf("stdout not capped cleanly: truncated=%v len=%d", r.StdoutTruncated, len(r.Stdout))
	}
	if !utf8Valid(r.Stdout) {
		t.Error("cap split a UTF-8 sequence")
	}
	if r.StdoutSHA256 != SHA256Hex(long) {
		t.Error("stdout hash does not cover the full output")
	}
	if r.StderrTruncated || r.Stderr != "err" || r.Args == nil {
		t.Errorf("short stderr changed: %+v", r)
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }

func TestHashWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(outside, []byte("host secret"), 0o600)
	_ = os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("abc"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "sub", "deep", "b.bin"), []byte{0, 1, 2}, 0o644)
	_ = os.Symlink(outside, filepath.Join(root, "leak"))
	_ = os.Symlink(filepath.Dir(outside), filepath.Join(root, "leakdir"))

	files, truncated, err := HashWorkspace(root)
	if err != nil || truncated {
		t.Fatalf("HashWorkspace: %v truncated=%v", err, truncated)
	}
	want := []File{
		{Path: "/a.txt", Size: 3, SHA256: SHA256Hex("abc")},
		{Path: "/leak", Symlink: true},
		{Path: "/leakdir", Symlink: true},
		{Path: "/sub/deep/b.bin", Size: 3, SHA256: SHA256Hex("\x00\x01\x02")},
	}
	if len(files) != len(want) {
		t.Fatalf("files = %+v", files)
	}
	for i, w := range want {
		f := files[i]
		if f.Path != w.Path || f.SHA256 != w.SHA256 || f.Symlink != w.Symlink || (!w.Symlink && f.Size != w.Size) {
			t.Errorf("file %d = %+v, want %+v", i, f, w)
		}
	}

	missing, _, err := HashWorkspace(filepath.Join(root, "gone"))
	if err != nil || len(missing) != 0 {
		t.Errorf("missing workspace: %v %v", missing, err)
	}
}

func TestCheckFindings(t *testing.T) {
	p := testPack()
	p.Runs[0].Stdout = "tampered before signing"
	p.Audit.Entries = append(p.Audit.Entries, AuditEntry{ID: "a2", Action: "command.started", SigStatus: "invalid", SigReason: "entry or signature was changed"})
	p.Audit.Scope = AuditScopeActor
	p.Session.Runtime = ""
	var msgs []string
	for _, f := range Check(p) {
		msgs = append(msgs, f.Severity+": "+f.Message)
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{
		"warning: run r1: stdout does not match",
		"warning: audit entry a2 (command.started) was invalid at export: entry or signature was changed",
		"info: audit entries are limited",
		"info: sandbox runtime not recorded",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing finding %q in:\n%s", want, joined)
		}
	}

	p = testPack()
	p.Audit.Signed = false
	if f := Check(p); len(f) != 1 || !strings.Contains(f[0].Message, "not signed by the server") {
		t.Errorf("unsigned audit: %+v", f)
	}
}
