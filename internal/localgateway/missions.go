package localgateway

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// MissionAPI persists successful tool sequences as VaultRun missions.
type MissionAPI interface {
	CreateMission(ctx context.Context, req createMissionRequest) (string, error)
	RecordMissionRun(ctx context.Context, missionID, sessionID string) error
}

type createMissionRequest struct {
	Slug        string        `json:"slug"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Version     string        `json:"version"`
	Steps       []missionStep `json:"steps"`
	Tags        []string      `json:"tags"`
	Published   bool          `json:"published"`
}

type missionStep struct {
	Name string            `json:"name"`
	Tool string            `json:"tool"`
	Args map[string]string `json:"args,omitempty"`
}

type httpMissionAPI struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func newHTTPMissionAPI(baseURL, apiKey string) *httpMissionAPI {
	return &httpMissionAPI{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *httpMissionAPI) CreateMission(ctx context.Context, req createMissionRequest) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/missions", req, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("mission create returned empty id")
	}
	return out.ID, nil
}

func (c *httpMissionAPI) RecordMissionRun(ctx context.Context, missionID, sessionID string) error {
	body := map[string]any{}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	path := "/api/v1/missions/" + url.PathEscape(missionID) + "/runs"
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}

func (c *httpMissionAPI) doJSON(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("missions API %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type capturedStep struct {
	Name      string
	Tool      string
	ArgsJSON  string
	ResultOK  bool
}

// captureMissionBestEffort creates a published mission + run from executed tools.
// Failures are logged only — never fail the chat response.
func (g *Gateway) captureMissionBestEffort(ctx context.Context, conversationKey, sessionID string, steps []capturedStep) {
	if !g.cfg.CaptureMissions || g.missions == nil || len(steps) == 0 {
		return
	}
	ms := make([]missionStep, 0, len(steps))
	for i, s := range steps {
		if !s.ResultOK {
			continue
		}
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("step-%d", i+1)
		}
		args := map[string]string{"arguments": truncate(s.ArgsJSON, 4000)}
		ms = append(ms, missionStep{Name: name, Tool: s.Tool, Args: args})
	}
	if len(ms) == 0 {
		return
	}

	slug := missionSlug(conversationKey, sessionID, ms)
	name := "Local gateway capture"
	if conversationKey != "" {
		name = "Local gateway: " + truncate(conversationKey, 80)
	}
	id, err := g.missions.CreateMission(ctx, createMissionRequest{
		Slug:        slug,
		Name:        name,
		Description: "Auto-captured from VaultRun local inference gateway tool loop",
		Version:     "1",
		Steps:       ms,
		Tags:        []string{"local-gateway", "auto-capture"},
		Published:   true,
	})
	if err != nil {
		slog.Warn("localgateway: mission capture failed", "err", err)
		return
	}
	if err := g.missions.RecordMissionRun(ctx, id, sessionID); err != nil {
		slog.Warn("localgateway: mission run record failed", "mission_id", id, "err", err)
		return
	}
	slog.Info("localgateway: mission captured", "mission_id", id, "steps", len(ms), "session_id", sessionID)
}

func missionSlug(conversationKey, sessionID string, steps []missionStep) string {
	h := sha1.New()
	_, _ = io.WriteString(h, conversationKey+"|"+sessionID+"|")
	for _, s := range steps {
		_, _ = io.WriteString(h, s.Tool+"|"+s.Args["arguments"]+"|")
	}
	sum := hex.EncodeToString(h.Sum(nil))[:12]
	base := "local"
	if conversationKey != "" {
		base = "local-" + slugify(conversationKey)
	}
	slug := base + "-" + sum
	if len(slug) > 100 {
		slug = slug[:100]
		slug = strings.Trim(slug, "-")
	}
	return slug
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "run"
	}
	if len(out) > 40 {
		out = out[:40]
		out = strings.Trim(out, "-")
	}
	return out
}
