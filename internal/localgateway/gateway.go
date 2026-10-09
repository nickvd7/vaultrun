package localgateway

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/nickvd7/vaultrun/internal/jev"
	vaultrun "github.com/nickvd7/vaultrun/sdk/go"
)

// VaultRunClient is the subset of the Go SDK used by the gateway.
type VaultRunClient interface {
	CreateSession(ctx context.Context, opts vaultrun.CreateSessionOptions) (*vaultrun.Session, error)
	GetSession(ctx context.Context, sessionID string) (*vaultrun.Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	Run(ctx context.Context, sessionID string, opts vaultrun.RunOptions) (*vaultrun.Run, error)
	UploadFile(ctx context.Context, sessionID, remotePath string, content io.Reader) (*vaultrun.File, error)
	DownloadFile(ctx context.Context, sessionID, remotePath string) (io.ReadCloser, error)
	ListFiles(ctx context.Context, sessionID string) ([]*vaultrun.File, error)
}

// Gateway is the local inference action plane.
type Gateway struct {
	cfg      Config
	vr       VaultRunClient
	upstream ChatUpstream
	missions MissionAPI
	jev      *jev.Client // nil when completion gate disabled
	sessions *SessionStore
	limiter  *ipRateLimiter
}

// New constructs a Gateway.
func New(cfg Config, vr VaultRunClient, upstream ChatUpstream) *Gateway {
	return &Gateway{
		cfg:      cfg,
		vr:       vr,
		upstream: upstream,
		sessions: NewSessionStore(),
		limiter:  newIPRateLimiter(cfg.RateLimitPerMin),
	}
}

// WithMissions attaches a mission capture backend (optional).
func (g *Gateway) WithMissions(m MissionAPI) *Gateway {
	g.missions = m
	return g
}

// WithJev attaches a TypeSafe Jev client for completion gating (optional).
func (g *Gateway) WithJev(c *jev.Client) *Gateway {
	g.jev = c
	return g
}

// NewFromConfig builds VaultRun + upstream clients from config.
func NewFromConfig(cfg Config) *Gateway {
	vr := vaultrun.New(cfg.VaultRunBaseURL, cfg.VaultRunAPIKey)
	up := NewHTTPUpstream(cfg.UpstreamURL, cfg.UpstreamTimeout)
	g := New(cfg, vr, up)
	if cfg.CaptureMissions {
		g.WithMissions(newHTTPMissionAPI(cfg.VaultRunBaseURL, cfg.VaultRunAPIKey))
	}
	if cfg.JevEnabled {
		jc, err := jev.ConfigFromEnv(os.Getenv)
		if err != nil {
			// Fail closed at request time via maybeJevCompletionGate when jev==nil && enabled.
			slog.Error("localgateway: LOCAL_GATEWAY_JEV_ENABLED=true but Jev client not configured — completions will be blocked", "err", err)
		} else {
			g.WithJev(jc)
			slog.Info("localgateway: Jev completion gate enabled",
				"provider", jc.Provider, "base_url", jc.BaseURL, "min_noul", cfg.JevMinNoul, "on_fail", cfg.JevOnFail)
		}
	}
	return g
}
