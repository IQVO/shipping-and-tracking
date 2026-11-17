package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/http"
)

// TestWrongMethodCarriesAllowHeader pins RFC 9110 section 15.5.6: a 405 MUST
// list the methods the resource does support. chi sets Allow only in its
// DEFAULT 405 handler; once the RFC 7807 handler is registered it is called
// as-is, so the handler derives the list from the router itself.
func TestWrongMethodCarriesAllowHeader(t *testing.T) {
	h := inbound.NewRouter(&inbound.Server{}, nil, "")
	cases := []struct{ method, path, wantAllow string }{
		{http.MethodDelete, "/healthz", "GET"},
		{http.MethodPost, "/readyz", "GET"},
		{http.MethodDelete, "/labels", "POST"},
		{http.MethodGet, "/labels", "POST"},
		{http.MethodGet, "/labels/lbl-1/voids", "POST"},
		{http.MethodPut, "/journeys/TRK-1", "GET"},
		{http.MethodGet, "/journeys/TRK-1/scans", "POST"},
		{http.MethodDelete, "/carrier-events", "POST"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, http.NoBody))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != tc.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tc.wantAllow)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
		})
	}
}

// TestUnknownPathCarriesNoAllowHeader: a 404 is not a 405; it lists nothing.
func TestUnknownPathCarriesNoAllowHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	inbound.NewRouter(&inbound.Server{}, nil, "").ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/nope", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "" {
		t.Errorf("Allow = %q, want empty on a 404", got)
	}
}
