package oneedu

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"admin-bot/internal/domain"
)

func makeJWT(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + payload + "." + sig
}

func TestParseJWTExpiry_Valid(t *testing.T) {
	want := time.Unix(1_900_000_000, 0)
	tok := makeJWT(t, map[string]interface{}{"exp": want.Unix(), "sub": "x"})

	got, err := parseJWTExpiry(tok)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("parseJWTExpiry: got %v, want %v", got, want)
	}
}

func TestParseJWTExpiry_HandlesUnpaddedBase64URL(t *testing.T) {
	// 5-char payload would need padding under StdEncoding. RawURLEncoding handles it.
	// We deliberately exercise the case by using a small payload.
	tok := makeJWT(t, map[string]interface{}{"exp": 12345})
	if _, err := parseJWTExpiry(tok); err != nil {
		t.Fatalf("parseJWTExpiry on small payload failed: %v", err)
	}
}

func TestParseJWTExpiry_InvalidShape(t *testing.T) {
	cases := []string{
		"",
		"abc",
		"only.two",
		"way.too.many.parts.here",
	}
	for _, c := range cases {
		c := c
		t.Run("malformed/"+c, func(t *testing.T) {
			if _, err := parseJWTExpiry(c); err == nil {
				t.Errorf("expected error for %q, got nil", c)
			}
		})
	}
}

func TestParseJWTExpiry_BadBase64(t *testing.T) {
	tok := "header." + "!!!not_base64!!!" + ".sig"
	if _, err := parseJWTExpiry(tok); err == nil {
		t.Error("expected base64 decode error, got nil")
	}
}

func TestParseJWTExpiry_NoExpClaim(t *testing.T) {
	tok := makeJWT(t, map[string]interface{}{"sub": "x"})
	if _, err := parseJWTExpiry(tok); err == nil {
		t.Error("expected error when exp claim missing, got nil")
	}
}

func TestExtractToken(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"raw", "abc.def.ghi", "abc.def.ghi"},
		{"trailingNewline", "abc.def.ghi\n", "abc.def.ghi"},
		{"jsonQuoted", `"abc.def.ghi"`, "abc.def.ghi"},
		{"whitespaceAndQuotes", "  \"abc.def.ghi\"  \n", "abc.def.ghi"},
		{"empty", "", ""},
		{"justQuotes", `""`, ""},
		// Edge case: token contains an internal quote — preserved.
		{"internalQuote", `ab"cd.ef.gh`, `ab"cd.ef.gh`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := extractToken([]byte(tc.in))
			if got != tc.want {
				t.Errorf("extractToken(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseJWTExpiry_MentionsThreePartsInError(t *testing.T) {
	_, err := parseJWTExpiry("a.b")
	if err == nil || !strings.Contains(err.Error(), "3 parts") {
		t.Errorf("error should mention expected part count, got: %v", err)
	}
}

func TestBuildRegionStatsVariables(t *testing.T) {
	loc := time.FixedZone("ALMT", 6*60*60)
	start := time.Date(2025, 6, 25, 0, 0, 0, 0, loc)
	end := time.Date(2026, 6, 30, 23, 59, 59, 0, loc)

	got := buildRegionStatsVariables("shymkent", start, end)

	want := map[string]interface{}{
		"campus":           "shymkent",
		"startDate":        "2025-06-25T00:00:00+06:00",
		"endDate":          "2026-06-30T23:59:59+06:00",
		"adminRole":        "campus_admin_shymkent",
		"gamesPath":        "/shymkent/onboarding/games",
		"checkinPathRegex": "^/shymkent/onboarding/check-?in$",
		"corePath":         "/shymkent/module",
	}

	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Errorf("%s = %v, want %v", key, got[key], wantValue)
		}
	}
}

// TestCheckinPathPattern covers the spelling difference that made the Pavlodar
// check-in count read 0: its path uses a hyphen, every other campus does not.
func TestCheckinPathPattern(t *testing.T) {
	cases := []struct {
		region string
		path   string
		want   bool
	}{
		{"pavlodar", "/pavlodar/onboarding/check-in", true},
		{"pavlodar", "/pavlodar/onboarding/checkin", true},
		{"shymkent", "/shymkent/onboarding/checkin", true},
		{"shymkent", "/shymkent/onboarding/check-in", true},
		// Anchored: must not match a longer path or another campus.
		{"pavlodar", "/pavlodar/onboarding/check-in/extra", false},
		{"pavlodar", "/x/pavlodar/onboarding/check-in", false},
		{"pavlodar", "/semey/onboarding/checkin", false},
		{"pavlodar", "/pavlodar/onboarding/checkpoint", false},
	}

	for _, tc := range cases {
		re, err := regexp.Compile("(?i)" + checkinPathPattern(tc.region))
		if err != nil {
			t.Fatalf("checkinPathPattern(%q) is not a valid pattern: %v", tc.region, err)
		}
		if got := re.MatchString(tc.path); got != tc.want {
			t.Errorf("%s: match(%q) = %v, want %v", tc.region, tc.path, got, tc.want)
		}
	}
}

// TestExactPathPattern verifies a pinned event's path still matches only itself.
func TestExactPathPattern(t *testing.T) {
	re, err := regexp.Compile("(?i)" + exactPathPattern("/pavlodar/onboarding/check-in"))
	if err != nil {
		t.Fatalf("invalid pattern: %v", err)
	}
	if !re.MatchString("/pavlodar/onboarding/check-in") {
		t.Error("a pinned path must match itself")
	}
	if re.MatchString("/pavlodar/onboarding/checkin") {
		t.Error("a pinned path must not match a different spelling")
	}
}

func TestClassifyPinnedEvent(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	cases := []struct {
		name       string
		campus     string
		meta       *domain.EventMeta
		wantReason string
		wantPath   string
	}{
		{
			name:     "active event in region -> usable path",
			campus:   "astanahub",
			meta:     &domain.EventMeta{ID: 1, Path: "/astanahub/onboarding/checkin", EndAt: future},
			wantPath: "/astanahub/onboarding/checkin",
		},
		{
			name:       "missing event",
			campus:     "astanahub",
			meta:       nil,
			wantReason: "not found",
		},
		{
			name:       "belongs to another region",
			campus:     "astanahub",
			meta:       &domain.EventMeta{ID: 2, Path: "/shymkent/onboarding/checkin", EndAt: future},
			wantReason: "region mismatch",
		},
		{
			name:       "already ended",
			campus:     "astanahub",
			meta:       &domain.EventMeta{ID: 3, Path: "/astanahub/piscinego", EndAt: past},
			wantReason: "ended",
		},
		{
			name:     "region check skipped when path has no segment",
			campus:   "astanahub",
			meta:     &domain.EventMeta{ID: 4, Path: "", EndAt: future},
			wantPath: "",
		},
		{
			name:       "ended exactly at now is unusable",
			campus:     "astanahub",
			meta:       &domain.EventMeta{ID: 5, Path: "/astanahub/piscinego", EndAt: now},
			wantReason: "ended",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPinnedEvent(tc.campus, tc.meta, now)
			if got.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", got.Path, tc.wantPath)
			}
		})
	}
}

func TestMapRegionUpdates_MissingAggregate(t *testing.T) {
	data := regionUpdatesNode{
		SignedUpNoOnboarding: strictAggregateCountNode{Aggregate: &countNode{Count: 1}},
		Succeeded:            strictAggregateCountNode{Aggregate: &countNode{Count: 2}},
		Checkin:              strictAggregateCountNode{Aggregate: &countNode{Count: 3}},
	}

	if _, err := mapRegionUpdates("shymkent", data); err == nil {
		t.Fatal("expected missing aggregate error, got nil")
	}
}
