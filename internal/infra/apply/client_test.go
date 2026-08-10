package apply

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(baseURL string, loc *time.Location) *Client {
	return NewClient(baseURL, "secret-token", loc, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestGetLeadCountsByCampus_ParsesTotalsAndAuth(t *testing.T) {
	const body = `{
		"total": 3,
		"by_campus": [
			{"campus_id": "Shymkent", "name_ru": "Шымкент", "total": 196},
			{"campus_id": "semey", "name_ru": "Семей", "total": 76},
			{"campus_id": "  ", "total": 5}
		],
		"leads": []
	}`

	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	counts, err := testClient(srv.URL, time.UTC).GetLeadCountsByCampus(context.Background())
	if err != nil {
		t.Fatalf("GetLeadCountsByCampus: %v", err)
	}

	if gotPath != exportPath {
		t.Errorf("request path = %q, want %q", gotPath, exportPath)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", gotAuth)
	}
	if len(counts) != 2 {
		t.Fatalf("counts = %v, want 2 entries (blank campus_id dropped)", counts)
	}
	if counts["shymkent"].Total != 196 {
		t.Errorf("shymkent total = %d, want 196 (key must be lowercased)", counts["shymkent"].Total)
	}
	if counts["semey"].Total != 76 {
		t.Errorf("semey total = %d, want 76", counts["semey"].Total)
	}
}

// TestAggregateByCampus_DailyBuckets verifies today/yesterday bucketing is done
// in the given location and that the all-time total comes from by_campus.
func TestAggregateByCampus_DailyBuckets(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	// "now" is 2026-07-29 10:00 Almaty. Today = 29 Jul, yesterday = 28 Jul (Almaty).
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, loc)

	mk := func(campus string, t time.Time) struct {
		CampusID  string    `json:"campus_id"`
		CreatedAt time.Time `json:"created_at"`
	} {
		return struct {
			CampusID  string    `json:"campus_id"`
			CreatedAt time.Time `json:"created_at"`
		}{CampusID: campus, CreatedAt: t}
	}

	export := exportResponse{}
	export.ByCampus = []struct {
		CampusID string `json:"campus_id"`
		Total    int    `json:"total"`
	}{
		{CampusID: "shymkent", Total: 196},
	}
	export.Leads = append(export.Leads,
		// Two today (one early morning Almaty, one late — both 29 Jul locally).
		mk("Shymkent", time.Date(2026, 7, 29, 0, 30, 0, 0, loc)),
		mk("shymkent", time.Date(2026, 7, 29, 9, 0, 0, 0, loc)),
		// One yesterday (28 Jul).
		mk("shymkent", time.Date(2026, 7, 28, 23, 0, 0, 0, loc)),
		// One older — neither today nor yesterday.
		mk("shymkent", time.Date(2026, 7, 20, 12, 0, 0, 0, loc)),
		// A lead near midnight expressed in a different offset (+00:00) that is
		// still 29 Jul once converted to Almaty (05:00 local) → counts as today.
		mk("shymkent", time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)),
	)

	got := aggregateByCampus(export, now, loc)
	sh := got["shymkent"]
	if sh.Total != 196 {
		t.Errorf("total = %d, want 196 (from by_campus)", sh.Total)
	}
	if sh.Today != 3 {
		t.Errorf("today = %d, want 3", sh.Today)
	}
	if sh.Yesterday != 1 {
		t.Errorf("yesterday = %d, want 1", sh.Yesterday)
	}
}

func TestGetLeadCountsByCampus_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, err := testClient(srv.URL, time.UTC).GetLeadCountsByCampus(context.Background()); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

func TestGetLeadCountsByCampus_TrailingSlashBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != exportPath {
			t.Errorf("path = %q, want %q (no double slash)", r.URL.Path, exportPath)
		}
		_, _ = io.WriteString(w, `{"by_campus":[],"leads":[]}`)
	}))
	defer srv.Close()

	// A base URL with a trailing slash must not produce "//api/export.json".
	c := testClient(srv.URL+"/", time.UTC)
	if _, err := c.GetLeadCountsByCampus(context.Background()); err != nil {
		t.Fatalf("GetLeadCountsByCampus: %v", err)
	}
}

func TestScrubRedactsToken(t *testing.T) {
	c := testClient("https://example.test", time.UTC)
	err := c.scrub(errChain("dial failed with token secret-token in url"))
	if strings.Contains(err.Error(), "secret-token") {
		t.Errorf("scrub left token in error: %q", err)
	}
}

type errChain string

func (e errChain) Error() string { return string(e) }
