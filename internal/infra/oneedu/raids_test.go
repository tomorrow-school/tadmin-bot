package oneedu

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"admin-bot/internal/domain"
)

// raidQueryServer stands in for the 01-edu GraphQL endpoint. It routes on the
// operation name embedded in the query text and records which operations were
// asked for, so a test can assert whether the fallback fired.
type raidQueryServer struct {
	mu       sync.Mutex
	calls    []string
	handlers map[string]string // operation name → JSON response body
}

func newRaidQueryServer(handlers map[string]string) (*raidQueryServer, *httptest.Server) {
	s := &raidQueryServer{handlers: handlers}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query := string(body)

		s.mu.Lock()
		defer s.mu.Unlock()
		for op, resp := range s.handlers {
			if strings.Contains(query, "query "+op+"(") {
				s.calls = append(s.calls, op)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp))
				return
			}
		}
		http.Error(w, `{"errors":[{"message":"unexpected operation"}]}`, http.StatusBadRequest)
	}))
	return s, srv
}

func (s *raidQueryServer) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// newRaidTestClient points a client at the test server with a pre-seeded JWT, so
// runQuery skips token acquisition.
func newRaidTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c := NewClient(baseURL, "access-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.jwtToken = "pre-seeded"
	c.jwtExp = time.Now().Add(time.Hour)
	return c
}

const emptyRaidsJSON = `{"data":{"event":[]}}`

// raidJSON builds a one-raid response.
func raidJSON(id int, name string) string {
	return `{"data":{"event":[{"id":` + itoa(id) + `,"object":{"type":"raid","name":"` + name +
		`"},"parentId":792,"groups":[{"captain":{"login":"a"},"group_status":{"status":"audit"},"members":[{"id":1,"userLogin":"a"}]}],` +
		`"startAt":"2026-08-17T09:00:00Z","endAt":"2026-08-21T18:00:00Z"}]}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestGetRaidsByPiscineID_FallsBackWhenNameFilterMatchesNothing is the regression
// test for "/edit_tables says Piscine AI 1 has no raids while /week shows one":
// GetRaidsByPiscineAiId filters on the hardcoded names backtesting-sp500 /
// forest-prediction, so a cohort running wine-pca-analysis matched nothing.
func TestGetRaidsByPiscineID_FallsBackWhenNameFilterMatchesNothing(t *testing.T) {
	server, srv := newRaidQueryServer(map[string]string{
		"GetRaidsByPiscineAiId": emptyRaidsJSON,
		"GetRaidsByParentId":    raidJSON(1234, "wine-pca-analysis"),
	})
	defer srv.Close()

	c := newRaidTestClient(t, srv.URL)
	raids, err := c.GetRaidsByPiscineID(context.Background(), domain.PiscineAI_1, 792)
	if err != nil {
		t.Fatalf("GetRaidsByPiscineID: %v", err)
	}

	if len(raids) != 1 {
		t.Fatalf("got %d raids, want 1 via the fallback query", len(raids))
	}
	if raids[0].RaidName != "wine-pca-analysis" {
		t.Errorf("RaidName = %q, want wine-pca-analysis", raids[0].RaidName)
	}
	// The piscine must survive the fallback, otherwise downstream sheet routing
	// (which is per piscine) loses track of the raid's owner.
	if raids[0].Piscine != domain.PiscineAI_1 {
		t.Errorf("Piscine = %q, want %q", raids[0].Piscine, domain.PiscineAI_1)
	}
	if raids[0].TeamsCount != 1 {
		t.Errorf("TeamsCount = %d, want 1", raids[0].TeamsCount)
	}

	want := []string{"GetRaidsByPiscineAiId", "GetRaidsByParentId"}
	got := server.called()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("queries called = %v, want %v (filtered first, then the fallback)", got, want)
	}
}

// TestGetRaidsByPiscineID_NoFallbackWhenFilterMatches verifies the filtered query
// still wins when it works, so Go/JS keep their name→week mapping.
func TestGetRaidsByPiscineID_NoFallbackWhenFilterMatches(t *testing.T) {
	server, srv := newRaidQueryServer(map[string]string{
		"GetRaidsByPiscineGoId": raidJSON(1, "quadchecker"),
		"GetRaidsByParentId":    raidJSON(2, "should-not-be-used"),
	})
	defer srv.Close()

	c := newRaidTestClient(t, srv.URL)
	raids, err := c.GetRaidsByPiscineID(context.Background(), domain.PiscineGo, 582)
	if err != nil {
		t.Fatalf("GetRaidsByPiscineID: %v", err)
	}

	if len(raids) != 1 || raids[0].RaidName != "quadchecker" {
		t.Fatalf("got %+v, want the single quadchecker raid", raids)
	}
	// quadchecker is in RaidWeekMap, so the week comes from the mapping.
	if raids[0].WeekNumber != 3 {
		t.Errorf("WeekNumber = %d, want 3 from RaidWeekMap", raids[0].WeekNumber)
	}
	if got := server.called(); len(got) != 1 || got[0] != "GetRaidsByPiscineGoId" {
		t.Errorf("queries called = %v, want only the filtered query", got)
	}
}

// TestGetRaidsByPiscineID_GenericQueryNotRetried verifies a piscine that already
// resolves to the unfiltered query (RUST) does not run it twice when the piscine
// genuinely has no raids.
func TestGetRaidsByPiscineID_GenericQueryNotRetried(t *testing.T) {
	server, srv := newRaidQueryServer(map[string]string{
		"GetRaidsByParentId": emptyRaidsJSON,
	})
	defer srv.Close()

	c := newRaidTestClient(t, srv.URL)
	raids, err := c.GetRaidsByPiscineID(context.Background(), domain.PiscineRUST, 999)
	if err != nil {
		t.Fatalf("GetRaidsByPiscineID: %v", err)
	}
	if len(raids) != 0 {
		t.Errorf("got %d raids, want none", len(raids))
	}
	if got := server.called(); len(got) != 1 {
		t.Errorf("queries called = %v, want exactly one request", got)
	}
}

// TestGetRaidsByParentID_UsesGenericQuery pins the path-discovery route: no name
// filter, and no piscine attached (week numbers come from raid ordering).
func TestGetRaidsByParentID_UsesGenericQuery(t *testing.T) {
	server, srv := newRaidQueryServer(map[string]string{
		"GetRaidsByParentId": raidJSON(7, "titanic-survival"),
	})
	defer srv.Close()

	c := newRaidTestClient(t, srv.URL)
	raids, err := c.GetRaidsByParentID(context.Background(), 797)
	if err != nil {
		t.Fatalf("GetRaidsByParentID: %v", err)
	}
	if len(raids) != 1 || raids[0].RaidName != "titanic-survival" {
		t.Fatalf("got %+v, want the titanic-survival raid", raids)
	}
	if raids[0].Piscine != "" {
		t.Errorf("Piscine = %q, want empty for a path-discovered pool", raids[0].Piscine)
	}
	if raids[0].WeekNumber != 0 {
		t.Errorf("WeekNumber = %d, want 0 (assigned by the caller from ordering)", raids[0].WeekNumber)
	}
	if got := server.called(); len(got) != 1 || got[0] != "GetRaidsByParentId" {
		t.Errorf("queries called = %v, want only the generic query", got)
	}
}
