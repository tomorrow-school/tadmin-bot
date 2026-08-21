package telegram

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"admin-bot/internal/domain"
	"admin-bot/internal/infra/accessstore"
	"admin-bot/internal/usecase"
)

// TestIsAuthorized covers the private-chat / group / super-admin authorization
// matrix. Telegram guarantees chatID == userID for private chats, so an
// approved user is authorized in their DM with no per-chat allowlisting.
func TestIsAuthorized(t *testing.T) {
	const (
		superAdmin  int64 = 999
		approved    int64 = 100
		pending     int64 = 200
		rejected    int64 = 300
		unknown     int64 = 400
		allowedChat int64 = -100
		otherChat   int64 = -500
	)

	store, err := accessstore.New(filepath.Join(t.TempDir(), "access.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	uc := usecase.NewAccessUseCase(store)
	if _, err := uc.Approve(approved); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := uc.RequestAccess(pending, "", ""); err != nil {
		t.Fatalf("request pending: %v", err)
	}
	if _, err := uc.RequestAccess(rejected, "", ""); err != nil {
		t.Fatalf("request rejected: %v", err)
	}
	if _, err := uc.Reject(rejected); err != nil {
		t.Fatalf("reject: %v", err)
	}

	h := &Handler{
		accessUC:     uc,
		superAdminID: superAdmin,
		authorized:   map[int64]bool{allowedChat: true},
	}

	cases := []struct {
		name           string
		chatID, userID int64
		want           bool
	}{
		{"super_admin_in_group", allowedChat, superAdmin, true},
		{"super_admin_in_dm", superAdmin, superAdmin, true},
		{"super_admin_in_random_group", otherChat, superAdmin, true},
		{"approved_in_own_dm", approved, approved, true},
		{"approved_in_allowed_group", allowedChat, approved, true},
		{"approved_in_other_group", otherChat, approved, false},
		{"pending_in_dm", pending, pending, false},
		{"rejected_in_dm", rejected, rejected, false},
		{"unknown_in_dm", unknown, unknown, false},
		{"unknown_in_allowed_group", allowedChat, unknown, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := h.isAuthorized(tc.chatID, tc.userID); got != tc.want {
				t.Errorf("isAuthorized(%d, %d) = %v, want %v", tc.chatID, tc.userID, got, tc.want)
			}
		})
	}
}

func TestParsePiscineFromCallback(t *testing.T) {
	cases := []struct {
		name   string
		data   string
		prefix string
		want   string
	}{
		{"happy_path", "defense_create:Piscine Go", "defense_create:", "Piscine Go"},
		{"different_prefix", "defense_edit:Piscine JS", "defense_edit:", "Piscine JS"},
		{"missing_prefix", "wrong:Piscine Go", "defense_create:", ""},
		{"empty_data", "", "defense_create:", ""},
		{"prefix_only", "defense_create:", "defense_create:", ""},
		{"value_contains_colon", "defense_create:Piscine: AI", "defense_create:", "Piscine: AI"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := parsePiscineFromCallback(tc.data, tc.prefix)
			if got != tc.want {
				t.Errorf("parsePiscineFromCallback(%q, %q) = %q, want %q",
					tc.data, tc.prefix, got, tc.want)
			}
		})
	}
}

// TestResolveSpreadsheetID verifies the routing rule: Go/JS/AI1/AI2/AI3 use
// their dedicated per-week sheet, while Piscine RUST (and any non-dedicated
// piscine) falls back to the shared universal table.
func TestResolveSpreadsheetID(t *testing.T) {
	const universalID = "UNIVERSAL_SHEET_ID"
	h := &Handler{
		universalSheetID: universalID,
		sheetIDs: map[domain.PiscineType]map[int]string{
			domain.PiscineGo:   {1: "GO_W1"},
			domain.PiscineJS:   {2: "JS_W2"},
			domain.PiscineAI_1: {1: "AI1_W1"},
			domain.PiscineAI_2: {3: "AI2_W3"},
			domain.PiscineAI_3: {1: "AI3_W1"},
		},
	}

	cases := []struct {
		name          string
		piscine       domain.PiscineType
		week          int
		wantID        string
		wantDedicated bool
	}{
		{"go_dedicated", domain.PiscineGo, 1, "GO_W1", true},
		{"js_dedicated", domain.PiscineJS, 2, "JS_W2", true},
		{"ai1_dedicated", domain.PiscineAI_1, 1, "AI1_W1", true},
		{"ai2_dedicated", domain.PiscineAI_2, 3, "AI2_W3", true},
		{"ai3_dedicated", domain.PiscineAI_3, 1, "AI3_W1", true},
		{"dedicated_but_week_unset", domain.PiscineGo, 9, "", true},
		{"rust_universal", domain.PiscineRUST, 1, universalID, false},
		{"unknown_universal", domain.PiscineType("Piscine Foo"), 2, universalID, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotDedicated := h.resolveSpreadsheetID(tc.piscine, tc.week)
			if gotID != tc.wantID {
				t.Errorf("id = %q, want %q", gotID, tc.wantID)
			}
			if gotDedicated != tc.wantDedicated {
				t.Errorf("dedicated = %v, want %v", gotDedicated, tc.wantDedicated)
			}
		})
	}
}

func TestParseTimeRange(t *testing.T) {
	cases := []struct {
		in             string
		wantOK         bool
		sh, sm, eh, em int
	}{
		{"11:00-17:00", true, 11, 0, 17, 0},
		{" 09:30 - 12:45 ", true, 9, 30, 12, 45},
		{"11:00-11:00", false, 0, 0, 0, 0}, // non-increasing
		{"17:00-11:00", false, 0, 0, 0, 0}, // reversed
		{"25:00-26:00", false, 0, 0, 0, 0}, // hour out of range
		{"11:60-12:00", false, 0, 0, 0, 0}, // minute out of range
		{"1100-1700", false, 0, 0, 0, 0},   // missing separators
		{"11:00", false, 0, 0, 0, 0},       // missing end
		{"", false, 0, 0, 0, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			sh, sm, eh, em, ok := parseTimeRange(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if sh != tc.sh || sm != tc.sm || eh != tc.eh || em != tc.em {
				t.Errorf("got %02d:%02d-%02d:%02d, want %02d:%02d-%02d:%02d",
					sh, sm, eh, em, tc.sh, tc.sm, tc.eh, tc.em)
			}
		})
	}
}

func TestBuildCapacityWarning(t *testing.T) {
	// Enough capacity → no warning.
	if got := buildCapacityWarning("11:00-17:00", 3, 30, 36, 30); got != "" {
		t.Errorf("expected no warning when capacity suffices, got %q", got)
	}
	// Capacity exactly equals teams → no warning.
	if got := buildCapacityWarning("11:00-17:00", 3, 30, 30, 30); got != "" {
		t.Errorf("expected no warning when capacity equals teams, got %q", got)
	}
	// Platform reports no teams → no warning (window drives the table).
	if got := buildCapacityWarning("11:00-17:00", 1, 30, 12, 0); got != "" {
		t.Errorf("expected no warning when teams==0, got %q", got)
	}
	// Shortfall: fewer slots than teams → warning mentioning the numbers.
	got := buildCapacityWarning("11:00-14:00", 3, 40, 12, 30)
	if got == "" {
		t.Fatal("expected a capacity shortfall warning")
	}
	for _, want := range []string{"11:00-14:00", "3 колон", "40 мин", "12 мест", "30"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q missing %q", got, want)
		}
	}
}

// TestNextMonday verifies the helper returns the *next* Monday (never today)
// at 00:00 in the input's location, for every weekday.
func TestNextMonday(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name   string
		in     time.Time
		wantDM string // YYYY-MM-DD
	}{
		// 2024-01-01 was a Monday.
		{"monday_morning_returns_next_monday", time.Date(2024, 1, 1, 9, 0, 0, 0, loc), "2024-01-08"},
		{"tuesday", time.Date(2024, 1, 2, 9, 0, 0, 0, loc), "2024-01-08"},
		{"wednesday", time.Date(2024, 1, 3, 9, 0, 0, 0, loc), "2024-01-08"},
		{"thursday", time.Date(2024, 1, 4, 9, 0, 0, 0, loc), "2024-01-08"},
		{"friday", time.Date(2024, 1, 5, 9, 0, 0, 0, loc), "2024-01-08"},
		{"saturday", time.Date(2024, 1, 6, 9, 0, 0, 0, loc), "2024-01-08"},
		{"sunday_returns_tomorrow", time.Date(2024, 1, 7, 9, 0, 0, 0, loc), "2024-01-08"},
		// Crossing a month boundary.
		{"end_of_month_friday", time.Date(2024, 1, 26, 9, 0, 0, 0, loc), "2024-01-29"},
		{"end_of_month_sunday", time.Date(2024, 1, 28, 9, 0, 0, 0, loc), "2024-01-29"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := nextMonday(tc.in)
			gotStr := got.Format("2006-01-02")
			if gotStr != tc.wantDM {
				t.Errorf("nextMonday(%s) = %s, want %s", tc.in.Format("2006-01-02 Mon"), gotStr, tc.wantDM)
			}
			if got.Weekday() != time.Monday {
				t.Errorf("nextMonday(%s) returned weekday %s, want Monday",
					tc.in.Format("2006-01-02 Mon"), got.Weekday())
			}
			if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
				t.Errorf("nextMonday(%s) should be midnight, got %v", tc.in, got)
			}
		})
	}
}

// TestNextMonday_PreservesInputLocation documents that the helper returns a time
// in the same location as its input. (See the "unresolved concerns" note about
// the implication for timezone-aware scheduling.)
func TestNextMonday_PreservesInputLocation(t *testing.T) {
	almaty, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Skip("Asia/Almaty tzdata not available")
	}
	in := time.Date(2024, 1, 7, 23, 0, 0, 0, almaty)
	got := nextMonday(in)
	if got.Location().String() != almaty.String() {
		t.Errorf("nextMonday returned location %q, want %q", got.Location(), almaty)
	}
}

func TestFormatRegionUpdatesMessage(t *testing.T) {
	got := formatRegionUpdatesMessage(domain.RegionUpdatesInfo{
		Region:                    "a<b",
		SignedUpWithoutOnboarding: 12,
		SucceededOnboardingGames:  34,
		CheckinRegistrations:      56,
		PiscineRegistrations: []domain.PiscineRegistrationCount{
			{Label: "ai-curriculum/prompt-piscine", Path: "/a<b/ai-curriculum/prompt-piscine", Count: 78},
		},
		CoreUsers: 90,
	}, "02.07.2026")

	wantParts := []string{
		"### 02.07.2026 - a&lt;b",
		"- 12 заявок",
		"- 34 прошли игры",
		"- 56 reg на check-in",
		"- 78 reg на ai-curriculum/prompt-piscine",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Errorf("formatted message missing %q:\n%s", part, got)
		}
	}
}

// TestFormatRegionUpdatesMessage_LeadApplications verifies the website-lead line
// is shown (with its own label, distinct from onboarding "заявок") only when the
// lead data is flagged present.
func TestFormatRegionUpdatesMessage_LeadApplications(t *testing.T) {
	withLeads := formatRegionUpdatesMessage(domain.RegionUpdatesInfo{
		Region:              "shymkent",
		LeadApplications:    domain.LeadCounts{Total: 196, Today: 6, Yesterday: 12},
		HasLeadApplications: true,
	}, "02.07.2026")
	if !strings.Contains(withLeads, "- 196 заявок с сайта (сегодня: 6, вчера: 12)\n") {
		t.Errorf("lead line missing/incorrect when present:\n%s", withLeads)
	}

	// Absent lead data: no lead line, even though the field defaults to 0.
	without := formatRegionUpdatesMessage(domain.RegionUpdatesInfo{
		Region: "shymkent",
	}, "02.07.2026")
	if strings.Contains(without, "заявок с сайта") {
		t.Errorf("lead line must be omitted when data absent:\n%s", without)
	}
}

// TestFormatRegionUpdatesMessage_StaleEvent verifies a metric backed by a stale
// pinned event (check-in) is shown as unavailable rather than as a (misleading)
// count.
func TestFormatRegionUpdatesMessage_StaleEvent(t *testing.T) {
	got := formatRegionUpdatesMessage(domain.RegionUpdatesInfo{
		Region:               "shymkent",
		CheckinRegistrations: 56,
		StaleEvents: []domain.StaleEvent{
			{Type: domain.EventCheckin, EventID: 111, Reason: "ended"},
		},
	}, "02.07.2026")

	if strings.Contains(got, "56 reg на check-in") {
		t.Errorf("stale check-in metric must not show its count:\n%s", got)
	}
	if !strings.Contains(got, "⚠️ reg на check-in") {
		t.Errorf("stale check-in metric should be flagged unavailable:\n%s", got)
	}
}

// TestFormatRegionUpdatesMessage_UpcomingPiscine verifies upcoming piscines are
// annotated with their start date.
func TestFormatRegionUpdatesMessage_UpcomingPiscine(t *testing.T) {
	got := formatRegionUpdatesMessage(domain.RegionUpdatesInfo{
		Region: "astanahub",
		PiscineRegistrations: []domain.PiscineRegistrationCount{
			{Label: "ai-curriculum/prompt-piscine", Count: 42},
			{Label: "module/piscine-rust", Count: 17, Upcoming: true, StartAt: time.Date(2026, 7, 6, 5, 0, 0, 0, time.UTC)},
		},
	}, "02.07.2026")

	if !strings.Contains(got, "- 42 reg на ai-curriculum/prompt-piscine\n") {
		t.Errorf("current piscine line missing:\n%s", got)
	}
	if !strings.Contains(got, "- 17 reg на module/piscine-rust (скоро старт: 06.07)") {
		t.Errorf("upcoming piscine line missing start annotation:\n%s", got)
	}
}

// TestParseTablesArg covers the /create_tables argument: no argument keeps the
// historical "every piscine" behavior, a known alias narrows to one piscine, and
// an unknown argument is rejected (rather than falling back to updating
// everything, which is the accident this command used to make easy).
func TestParseTablesArg(t *testing.T) {
	all := len(domain.AllPiscines())
	cases := []struct {
		in        string
		wantOK    bool
		wantCount int
		wantFirst domain.PiscineType
	}{
		{"/create_tables", true, all, domain.PiscineGo},
		{"/create_tables@tadmin_bot", true, all, domain.PiscineGo},
		{"/create_tables all", true, all, domain.PiscineGo},
		{"/create_tables все", true, all, domain.PiscineGo},
		{"/create_tables go", true, 1, domain.PiscineGo},
		{"/create_tables js", true, 1, domain.PiscineJS},
		{"/create_tables RUST", true, 1, domain.PiscineRUST},
		{"/create_tables ai2", true, 1, domain.PiscineAI_2},
		{"  /create_tables   rust  ", true, 1, domain.PiscineRUST},
		{"/create_tables python", false, 0, ""},
		{"/create_tables 3", false, 0, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			got, ok := parseTablesArg(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if len(got) != tc.wantCount {
				t.Fatalf("got %d piscines (%v), want %d", len(got), got, tc.wantCount)
			}
			if got[0] != tc.wantFirst {
				t.Errorf("first = %q, want %q", got[0], tc.wantFirst)
			}
		})
	}
}

// TestRejectConflictingTargets is the regression test for the data loss: two
// piscines resolving to one spreadsheet must NOT both be written, because the
// second write clears A1:Z1000 and replaces the first one's defense table.
func TestRejectConflictingTargets(t *testing.T) {
	raid := &domain.RaidInfo{RaidName: "r"}

	t.Run("distinct_documents_all_written", func(t *testing.T) {
		writable, lines := rejectConflictingTargets([]tableTarget{
			{piscine: domain.PiscineGo, spreadsheetID: "DOC_A", raid: raid},
			{piscine: domain.PiscineJS, spreadsheetID: "DOC_B", raid: raid},
		})
		if len(writable) != 2 {
			t.Errorf("writable = %d, want 2", len(writable))
		}
		if len(lines) != 0 {
			t.Errorf("unexpected warnings: %v", lines)
		}
	})

	t.Run("shared_document_blocks_both", func(t *testing.T) {
		writable, lines := rejectConflictingTargets([]tableTarget{
			{piscine: domain.PiscineRUST, spreadsheetID: "SHARED", raid: raid},
			{piscine: domain.PiscineGo, spreadsheetID: "SHARED", raid: raid},
		})
		if len(writable) != 0 {
			t.Fatalf("writable = %v, want none (both target one document)", writable)
		}
		if len(lines) != 1 {
			t.Fatalf("lines = %v, want exactly one explanation", lines)
		}
		for _, want := range []string{"Piscine RUST", "Piscine Go", "/create_tables rust", "/create_tables go"} {
			if !strings.Contains(lines[0], want) {
				t.Errorf("explanation %q missing %q", lines[0], want)
			}
		}
	})

	t.Run("conflict_does_not_block_unrelated_target", func(t *testing.T) {
		writable, lines := rejectConflictingTargets([]tableTarget{
			{piscine: domain.PiscineRUST, spreadsheetID: "SHARED", raid: raid},
			{piscine: domain.PiscineGo, spreadsheetID: "SHARED", raid: raid},
			{piscine: domain.PiscineJS, spreadsheetID: "DOC_B", raid: raid},
		})
		if len(writable) != 1 || writable[0].piscine != domain.PiscineJS {
			t.Errorf("writable = %v, want only Piscine JS", writable)
		}
		if len(lines) != 1 {
			t.Errorf("lines = %v, want one explanation", lines)
		}
	})

	t.Run("single_target_never_conflicts", func(t *testing.T) {
		writable, lines := rejectConflictingTargets([]tableTarget{
			{piscine: domain.PiscineRUST, spreadsheetID: "SHARED", raid: raid},
		})
		if len(writable) != 1 {
			t.Errorf("writable = %v, want the single target", writable)
		}
		if len(lines) != 0 {
			t.Errorf("unexpected warnings: %v", lines)
		}
	})
}

// TestSharedDocumentWarning verifies a write into a document configured in two
// SHEET_* slots is flagged — this is the .env case where SHEET_GO_WEEK1 and
// SHEET_GO_WEEK2 hold the same URL, so week 2 overwrites week 1's table.
func TestSharedDocumentWarning(t *testing.T) {
	h := &Handler{sheetSlots: map[string][]string{
		"SHARED_DOC": {"SHEET_GO_WEEK1", "SHEET_GO_WEEK2"},
		"OWN_DOC":    {"SHEET_JS_WEEK1"},
	}}

	got := h.sharedDocumentWarning("SHARED_DOC")
	if got == "" {
		t.Fatal("expected a warning for a document used by two slots")
	}
	for _, want := range []string{"SHEET_GO_WEEK1", "SHEET_GO_WEEK2"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q missing %q", got, want)
		}
	}
	if got := h.sharedDocumentWarning("OWN_DOC"); got != "" {
		t.Errorf("no warning expected for a dedicated document, got %q", got)
	}
	if got := h.sharedDocumentWarning("UNKNOWN"); got != "" {
		t.Errorf("no warning expected for an unknown document, got %q", got)
	}
}
