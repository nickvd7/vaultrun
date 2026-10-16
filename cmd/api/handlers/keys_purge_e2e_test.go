//go:build integration

package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestRevokedKeysCanBeDeletedAndWhoamiReportsRole(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	key, id := issueKey(t, r, "purge-"+uuid.NewString()[:8])

	who := func(hdr map[string]string) (master bool) {
		w := rec(r, "GET", "/api/v1/whoami", "", hdr)
		if w.Code != http.StatusOK {
			t.Fatalf("whoami: want 200, got %d: %s", w.Code, w.Body)
		}
		var out struct {
			Master bool `json:"master"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Master
	}
	if !who(masterHdr()) {
		t.Error("whoami: master key not reported as master")
	}
	if who(keyHdr(key)) {
		t.Error("whoami: regular key reported as master")
	}

	if w := rec(r, "DELETE", "/api/v1/keys/"+id+"?purge=true", "", masterHdr()); w.Code != http.StatusConflict {
		t.Fatalf("purging an active key: want 409, got %d: %s", w.Code, w.Body)
	}
	if w := rec(r, "DELETE", "/api/v1/keys/"+id, "", masterHdr()); w.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d: %s", w.Code, w.Body)
	}
	other, _ := issueKey(t, r, "other-"+uuid.NewString()[:8])
	if w := rec(r, "DELETE", "/api/v1/keys/"+id+"?purge=true", "", keyHdr(other)); w.Code != http.StatusForbidden {
		t.Fatalf("purge with a regular key: want 403, got %d", w.Code)
	}
	if w := rec(r, "DELETE", "/api/v1/keys/"+id+"?purge=true", "", masterHdr()); w.Code != http.StatusNoContent {
		t.Fatalf("purging a revoked key: want 204, got %d: %s", w.Code, w.Body)
	}
	if w := rec(r, "DELETE", "/api/v1/keys/"+id+"?purge=true", "", masterHdr()); w.Code != http.StatusNotFound {
		t.Fatalf("purging twice: want 404, got %d: %s", w.Code, w.Body)
	}

	var n int
	if err := testDB.Get(&n, `SELECT COUNT(*) FROM api_keys WHERE id = $1`, id); err != nil || n != 0 {
		t.Fatalf("key row still present (n=%d, err=%v)", n, err)
	}
}
