package secrets

import (
	"bytes"
	"encoding/base64"
	"io"
	"sort"
	"sync"
)

// MinRedactLen is the shortest secret value that is redacted. Shorter values
// would corrupt ordinary output (a secret "1" would blank every digit one).
const MinRedactLen = 4

type needle struct {
	pat  []byte
	repl []byte
}

// Redactor removes the exact values of secrets that were injected into a
// command from its output. It also matches the standard and URL-safe base64
// encodings of each value, since `echo $SECRET | base64` is the obvious way to
// get around a plain match. It cannot catch arbitrary transformations (hex,
// reversed, split across lines), so it limits accidental leaks; it is not a
// guarantee against a command that is trying to exfiltrate the value.
type Redactor struct {
	needles []needle
	first   [256]bool
	maxLen  int
}

// NewRedactor builds a redactor for the given secret name -> value map.
// It returns nil when there is nothing to redact; a nil Redactor is valid and
// is a no-op.
func NewRedactor(secrets map[string]string) *Redactor {
	r := &Redactor{}
	seen := map[string]bool{}
	add := func(name string, pat []byte) {
		if len(pat) == 0 || seen[string(pat)] {
			return
		}
		seen[string(pat)] = true
		r.needles = append(r.needles, needle{
			pat:  pat,
			repl: []byte("[REDACTED:" + name + "]"),
		})
	}
	for name, val := range secrets {
		if len(val) < MinRedactLen {
			continue
		}
		raw := []byte(val)
		add(name, raw)
		for _, enc := range []*base64.Encoding{
			base64.StdEncoding, base64.RawStdEncoding,
			base64.URLEncoding, base64.RawURLEncoding,
		} {
			add(name, []byte(enc.EncodeToString(raw)))
		}
	}
	if len(r.needles) == 0 {
		return nil
	}
	// Longest first so a secret that contains a shorter one is replaced whole.
	sort.Slice(r.needles, func(i, j int) bool {
		return len(r.needles[i].pat) > len(r.needles[j].pat)
	})
	for _, n := range r.needles {
		r.first[n.pat[0]] = true
		if len(n.pat) > r.maxLen {
			r.maxLen = len(n.pat)
		}
	}
	return r
}

// Redact returns s with every secret value replaced.
func (r *Redactor) Redact(s string) string {
	if r == nil || s == "" {
		return s
	}
	out, _ := r.process([]byte(s), true)
	return string(out)
}

// process scans buf and returns the redacted output plus the unprocessed tail.
// When final is false, a tail that could still turn into a match once more
// data arrives is held back.
func (r *Redactor) process(buf []byte, final bool) (out, rest []byte) {
	out = make([]byte, 0, len(buf))
	i := 0
	for i < len(buf) {
		if !r.first[buf[i]] {
			out = append(out, buf[i])
			i++
			continue
		}
		matched := false
		for _, n := range r.needles {
			if bytes.HasPrefix(buf[i:], n.pat) {
				out = append(out, n.repl...)
				i += len(n.pat)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if !final && r.couldExtend(buf[i:]) {
			return out, buf[i:]
		}
		out = append(out, buf[i])
		i++
	}
	return out, nil
}

// couldExtend reports whether tail is a strict prefix of some needle.
func (r *Redactor) couldExtend(tail []byte) bool {
	for _, n := range r.needles {
		if len(tail) < len(n.pat) && bytes.HasPrefix(n.pat, tail) {
			return true
		}
	}
	return false
}

// Writer wraps w so everything written through it is redacted, including
// secrets that arrive split across several Write calls. Close must be called
// once the command finished to flush the held-back tail.
func (r *Redactor) Writer(w io.Writer) io.WriteCloser {
	if r == nil {
		return nopCloser{w}
	}
	return &redactWriter{r: r, w: w}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type redactWriter struct {
	mu   sync.Mutex
	r    *Redactor
	w    io.Writer
	hold []byte
}

func (rw *redactWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	buf := append(rw.hold, p...)
	out, rest := rw.r.process(buf, false)
	rw.hold = append([]byte(nil), rest...)
	if len(out) > 0 {
		if _, err := rw.w.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (rw *redactWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if len(rw.hold) == 0 {
		return nil
	}
	out, _ := rw.r.process(rw.hold, true)
	rw.hold = nil
	_, err := rw.w.Write(out)
	return err
}
