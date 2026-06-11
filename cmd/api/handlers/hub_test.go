package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func newHubTestContext(target string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Params = params
	return c, w
}

func TestPaginationDefaults(t *testing.T) {
	c, _ := newHubTestContext("/x", nil)
	pg := pagination(c)
	if pg.limit != 50 || pg.page != 1 || pg.offset != 0 {
		t.Errorf("pagination defaults = %+v, want limit=50 page=1 offset=0", pg)
	}
}

func TestPaginationCustomValues(t *testing.T) {
	c, _ := newHubTestContext("/x?limit=10&page=3", nil)
	pg := pagination(c)
	if pg.limit != 10 || pg.page != 3 || pg.offset != 20 {
		t.Errorf("pagination = %+v, want limit=10 page=3 offset=20", pg)
	}
}

func TestPaginationClampsOutOfRangeLimit(t *testing.T) {
	cases := []struct {
		name  string
		query string
		limit int
	}{
		{"zero", "limit=0", 50},
		{"negative", "limit=-5", 50},
		{"too large", "limit=10000", 50},
		{"non-numeric", "limit=abc", 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newHubTestContext("/x?"+tc.query, nil)
			pg := pagination(c)
			if pg.limit != tc.limit {
				t.Errorf("limit = %d, want %d", pg.limit, tc.limit)
			}
		})
	}
}

func TestPaginationClampsOutOfRangePage(t *testing.T) {
	cases := []struct {
		name  string
		query string
		page  int
	}{
		{"zero", "page=0", 1},
		{"negative", "page=-3", 1},
		{"too large", "page=99999999", 1_000_000},
		{"non-numeric", "page=abc", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newHubTestContext("/x?"+tc.query, nil)
			pg := pagination(c)
			if pg.page != tc.page {
				t.Errorf("page = %d, want %d", pg.page, tc.page)
			}
		})
	}
}

func TestPaginationOffsetComputation(t *testing.T) {
	c, _ := newHubTestContext("/x?limit=25&page=4", nil)
	pg := pagination(c)
	if pg.offset != 75 {
		t.Errorf("offset = %d, want 75", pg.offset)
	}
}

func TestPaginationParamsResponse(t *testing.T) {
	pg := paginationParams{limit: 20, offset: 40, page: 3}
	resp := pg.response(123)
	if resp["page"] != 3 || resp["limit"] != 20 || resp["offset"] != 40 || resp["total"] != 123 {
		t.Errorf("response = %+v, want page=3 limit=20 offset=40 total=123", resp)
	}
}

func TestParseUUIDValid(t *testing.T) {
	id := uuid.New()
	c, w := newHubTestContext("/sessions/"+id.String(), gin.Params{{Key: "id", Value: id.String()}})

	got, ok := parseUUID(c, "id")
	if !ok {
		t.Fatal("expected parseUUID to succeed for a valid UUID")
	}
	if got != id {
		t.Errorf("parsed UUID = %v, want %v", got, id)
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected no response written on success, got status %d", w.Code)
	}
}

func TestParseUUIDInvalid(t *testing.T) {
	c, w := newHubTestContext("/sessions/not-a-uuid", gin.Params{{Key: "id", Value: "not-a-uuid"}})

	_, ok := parseUUID(c, "id")
	if ok {
		t.Fatal("expected parseUUID to fail for an invalid UUID")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if want := `{"error":"invalid id"}`; w.Body.String() != want+"\n" && w.Body.String() != want {
		t.Errorf("body = %q, want %q", w.Body.String(), want)
	}
}
