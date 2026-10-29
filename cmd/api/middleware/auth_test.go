package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// stubDB satisfies the *sqlx.DB parameter slot but is never used in the
// master-key path, so a nil pointer is safe for these tests.

func newAuthRouter(masterKey string) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery()) // catch nil-db panic in non-master-key path
	// Pass nil for db — master-key path never touches the DB.
	r.GET("/", APIKeyAuth(nil, masterKey, nil), func(c *gin.Context) {
		c.String(http.StatusOK, Actor(c))
	})
	return r
}

func TestMasterKeyAccepted(t *testing.T) {
	r := newAuthRouter("secret-master")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret-master")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "master" {
		t.Fatalf("expected actor=master, got %q", w.Body.String())
	}
}

func TestMasterKeyRejectedWhenWrong(t *testing.T) {
	r := newAuthRouter("secret-master")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// wrong key falls through to DB lookup which returns error (nil db → panic guard)
	// We only check that the master key path does NOT accept it.
	if w.Code == http.StatusOK {
		t.Fatal("wrong master key must not return 200")
	}
}

func TestMissingKeyReturns401(t *testing.T) {
	r := newAuthRouter("secret-master")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestBearerTokenExtracted(t *testing.T) {
	r := newAuthRouter("bearer-secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bearer-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestEmptyMasterKeyDisablesShortCircuit(t *testing.T) {
	// When masterKey is empty, the short-circuit must not fire —
	// even an empty header should go to DB lookup and fail.
	r := newAuthRouter("")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatal("empty master key must not grant access")
	}
}

type fakeSessions struct{ keyID string }

func (f fakeSessions) VerifyAPIKeyID(*gin.Context) (string, error) { return f.keyID, nil }

// A session cookie rides along on cross-site form posts; without the client
// header such a request must be refused before anything else happens.
func TestSessionCookieWriteNeedsClientHeader(t *testing.T) {
	r := gin.New()
	r.POST("/", APIKeyAuth(nil, "", fakeSessions{keyID: "k"}), func(c *gin.Context) { c.String(http.StatusOK, "reached") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without %s, got %d", ClientHeader, w.Code)
	}
}

func TestBodyLimit(t *testing.T) {
	r := gin.New()
	r.Use(BodyLimit(2 << 20))
	r.POST("/", func(c *gin.Context) {
		var v map[string]any
		if err := c.ShouldBindJSON(&v); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusOK)
	})
	big := `{"a":"` + strings.Repeat("x", MaxJSONBody) + `"}`
	for name, req := range map[string]*http.Request{
		"declared length": httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big)),
		"chunked": func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big))
			r.ContentLength = -1
			return r
		}(),
	} {
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("%s: a body over MaxJSONBody was accepted", name)
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("small body: %d", w.Code)
	}
}
