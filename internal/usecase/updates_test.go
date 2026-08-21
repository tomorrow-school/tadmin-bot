package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"admin-bot/internal/domain"
)

type fakeUpdatesClient struct {
	campuses    []string
	campusesErr error
	stats       map[string]*domain.RegionUpdatesInfo
	statsErr    map[string]error
	statsCalls  []string
	eventsSeen  map[string]domain.RegionUpdateEventsConfig
	events      map[int]*domain.EventMeta
	eventInfos  map[int]*domain.EventInfo

	current     []domain.PiscineEvent
	upcoming    []domain.PiscineEvent
	regByPath   map[string]int // path -> registration count
	pathCounted []string       // paths passed to GetRegistrationCountByPath
}

func (f *fakeUpdatesClient) RefreshToken(ctx context.Context) error {
	return nil
}

func (f *fakeUpdatesClient) GetCurrentPiscineID(ctx context.Context, piscine domain.PiscineType) (*domain.PiscineInfo, error) {
	return nil, nil
}

func (f *fakeUpdatesClient) GetRaidsByPiscineID(ctx context.Context, piscine domain.PiscineType, piscineEventID int) ([]domain.RaidInfo, error) {
	return nil, nil
}

func (f *fakeUpdatesClient) GetRaidByName(ctx context.Context, name string, startAt string) (*domain.RaidInfo, error) {
	return nil, nil
}

func (f *fakeUpdatesClient) GetCurrentPiscines(ctx context.Context) ([]domain.PiscineEvent, error) {
	return f.current, nil
}

func (f *fakeUpdatesClient) GetUpcomingPiscines(ctx context.Context) ([]domain.PiscineEvent, error) {
	return f.upcoming, nil
}

func (f *fakeUpdatesClient) GetRaidsByParentID(ctx context.Context, parentEventID int) ([]domain.RaidInfo, error) {
	return nil, nil
}

func (f *fakeUpdatesClient) GetRegistrationCountByPath(ctx context.Context, path string, endDate time.Time) (int, error) {
	f.pathCounted = append(f.pathCounted, path)
	return f.regByPath[path], nil
}

func (f *fakeUpdatesClient) GetAstanaUpdates(ctx context.Context) (*domain.AstanaUpdatesInfo, error) {
	return nil, nil
}

func (f *fakeUpdatesClient) GetCampuses(ctx context.Context) ([]string, error) {
	if f.campusesErr != nil {
		return nil, f.campusesErr
	}
	return f.campuses, nil
}

func (f *fakeUpdatesClient) GetEventByID(ctx context.Context, id int) (*domain.EventMeta, error) {
	return f.events[id], nil
}

func (f *fakeUpdatesClient) GetEventInfo(ctx context.Context, id int) (*domain.EventInfo, error) {
	return f.eventInfos[id], nil
}

func (f *fakeUpdatesClient) GetRegionUpdates(ctx context.Context, campus string, events domain.RegionUpdateEventsConfig) (*domain.RegionUpdatesInfo, error) {
	f.statsCalls = append(f.statsCalls, campus)
	if f.eventsSeen == nil {
		f.eventsSeen = map[string]domain.RegionUpdateEventsConfig{}
	}
	f.eventsSeen[campus] = events
	if err := f.statsErr[campus]; err != nil {
		return nil, err
	}
	return f.stats[campus], nil
}

// fakeLeadClient is a stub domain.LeadClient for the usecase tests.
type fakeLeadClient struct {
	counts map[string]domain.LeadCounts
	err    error
	calls  int
}

func (f *fakeLeadClient) GetLeadCountsByCampus(ctx context.Context) (map[string]domain.LeadCounts, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.counts, nil
}

// TestUpdatesUseCaseGetRegionUpdates_AttachesLeadCounts verifies lead counts are
// attached per campus (case-insensitively), that a campus absent from the map
// reports 0 with the data flagged present, and that the lead client is called
// exactly once for the whole report.
func TestUpdatesUseCaseGetRegionUpdates_AttachesLeadCounts(t *testing.T) {
	client := &fakeUpdatesClient{
		campuses: []string{"Shymkent", "semey"},
		stats: map[string]*domain.RegionUpdatesInfo{
			"Shymkent": {Region: "Shymkent"},
			"semey":    {Region: "semey"},
		},
	}
	leads := &fakeLeadClient{counts: map[string]domain.LeadCounts{
		"shymkent": {Total: 196, Today: 6, Yesterday: 12},
	}}

	report, err := NewUpdatesUseCase(client, nil, leads).GetRegionUpdates(context.Background())
	if err != nil {
		t.Fatalf("GetRegionUpdates: %v", err)
	}
	if leads.calls != 1 {
		t.Errorf("lead client calls = %d, want 1 (fetched once per report)", leads.calls)
	}

	byRegion := map[string]domain.RegionUpdatesInfo{}
	for _, r := range report.Regions {
		byRegion[r.Region] = r
	}

	shymkent := byRegion["Shymkent"]
	want := domain.LeadCounts{Total: 196, Today: 6, Yesterday: 12}
	if !shymkent.HasLeadApplications || shymkent.LeadApplications != want {
		t.Errorf("shymkent leads = %+v (has=%v), want %+v (has=true, case-insensitive)",
			shymkent.LeadApplications, shymkent.HasLeadApplications, want)
	}
	// semey is absent from the map: zero submissions, but data is still present.
	semey := byRegion["semey"]
	if !semey.HasLeadApplications || semey.LeadApplications != (domain.LeadCounts{}) {
		t.Errorf("semey leads = %+v (has=%v), want zeros (has=true)",
			semey.LeadApplications, semey.HasLeadApplications)
	}
}

// TestUpdatesUseCaseGetRegionUpdates_LeadFetchFailureIsBestEffort verifies a
// lead fetch error does not fail the report; regions are returned with the lead
// data flagged absent so the formatter omits the line.
func TestUpdatesUseCaseGetRegionUpdates_LeadFetchFailureIsBestEffort(t *testing.T) {
	client := &fakeUpdatesClient{
		campuses: []string{"shymkent"},
		stats:    map[string]*domain.RegionUpdatesInfo{"shymkent": {Region: "shymkent"}},
	}
	leads := &fakeLeadClient{err: errors.New("apply down")}

	report, err := NewUpdatesUseCase(client, nil, leads).GetRegionUpdates(context.Background())
	if err != nil {
		t.Fatalf("GetRegionUpdates returned fatal error: %v", err)
	}
	if len(report.Regions) != 1 {
		t.Fatalf("regions len = %d, want 1", len(report.Regions))
	}
	if report.Regions[0].HasLeadApplications {
		t.Errorf("HasLeadApplications = true, want false when lead fetch fails")
	}
}

// TestUpdatesUseCaseGetRegionUpdates_NoLeadClientOmitsLeads verifies that with
// no lead client configured the report leaves lead data absent.
func TestUpdatesUseCaseGetRegionUpdates_NoLeadClientOmitsLeads(t *testing.T) {
	client := &fakeUpdatesClient{
		campuses: []string{"shymkent"},
		stats:    map[string]*domain.RegionUpdatesInfo{"shymkent": {Region: "shymkent"}},
	}

	report, err := NewUpdatesUseCase(client, nil, nil).GetRegionUpdates(context.Background())
	if err != nil {
		t.Fatalf("GetRegionUpdates: %v", err)
	}
	if report.Regions[0].HasLeadApplications {
		t.Errorf("HasLeadApplications = true, want false with no lead client")
	}
}

// TestUpdatesUseCaseGetRegionUpdates_CampusAliasMatchesLeads verifies the
// 01-edu campus name "petropavlovsk" is matched to the apply campus_id
// "petropavl" so its lead counts aren't lost to the naming mismatch.
func TestUpdatesUseCaseGetRegionUpdates_CampusAliasMatchesLeads(t *testing.T) {
	client := &fakeUpdatesClient{
		campuses: []string{"petropavlovsk"},
		stats:    map[string]*domain.RegionUpdatesInfo{"petropavlovsk": {Region: "petropavlovsk"}},
	}
	leads := &fakeLeadClient{counts: map[string]domain.LeadCounts{
		"petropavl": {Total: 64, Today: 3, Yesterday: 8},
	}}

	report, err := NewUpdatesUseCase(client, nil, leads).GetRegionUpdates(context.Background())
	if err != nil {
		t.Fatalf("GetRegionUpdates: %v", err)
	}
	got := report.Regions[0].LeadApplications
	want := domain.LeadCounts{Total: 64, Today: 3, Yesterday: 8}
	if got != want {
		t.Errorf("petropavlovsk leads = %+v, want %+v (alias to petropavl)", got, want)
	}
}

func TestUpdatesUseCaseGetRegionUpdates_ContinuesAfterRegionError(t *testing.T) {
	client := &fakeUpdatesClient{
		campuses: []string{"shymkent", "semey"},
		stats: map[string]*domain.RegionUpdatesInfo{
			"shymkent": {
				Region:                    "shymkent",
				SignedUpWithoutOnboarding: 12,
				SucceededOnboardingGames:  34,
				CheckinRegistrations:      56,
				CoreUsers:                 90,
			},
		},
		statsErr: map[string]error{
			"semey": errors.New("upstream failed"),
		},
	}

	report, err := NewUpdatesUseCase(client, nil, nil).GetRegionUpdates(context.Background())
	if err != nil {
		t.Fatalf("GetRegionUpdates returned fatal error: %v", err)
	}

	if len(report.Regions) != 1 {
		t.Fatalf("regions len = %d, want 1", len(report.Regions))
	}
	if got := report.Regions[0].Region; got != "shymkent" {
		t.Errorf("region = %q, want shymkent", got)
	}
	if len(report.Errors) != 1 {
		t.Fatalf("errors len = %d, want 1", len(report.Errors))
	}
	if got := report.Errors[0].Region; got != "semey" {
		t.Errorf("error region = %q, want semey", got)
	}
	if got := client.statsCalls; len(got) != 2 || got[0] != "shymkent" || got[1] != "semey" {
		t.Errorf("stats calls = %v, want [shymkent semey]", got)
	}
}

func TestUpdatesUseCaseGetRegionUpdates_EmptyCampuses(t *testing.T) {
	_, err := NewUpdatesUseCase(&fakeUpdatesClient{}, nil, nil).GetRegionUpdates(context.Background())
	if !errors.Is(err, domain.ErrNoCampuses) {
		t.Fatalf("error = %v, want ErrNoCampuses", err)
	}
}

// TestUpdatesUseCaseGetRegionUpdates_PassesPinnedEvents verifies the usecase
// forwards each campus's pinned event config (zero-valued when unset).
func TestUpdatesUseCaseGetRegionUpdates_PassesPinnedEvents(t *testing.T) {
	client := &fakeUpdatesClient{
		campuses: []string{"shymkent", "semey"},
		stats: map[string]*domain.RegionUpdatesInfo{
			"shymkent": {Region: "shymkent"},
			"semey":    {Region: "semey"},
		},
	}
	regionEvents := map[string]domain.RegionUpdateEventsConfig{
		"shymkent": {CheckinEventID: 111, PiscineEventID: 222, ModuleEventID: 333},
	}

	if _, err := NewUpdatesUseCase(client, regionEvents, nil).GetRegionUpdates(context.Background()); err != nil {
		t.Fatalf("GetRegionUpdates returned error: %v", err)
	}

	if got := client.eventsSeen["shymkent"]; got != regionEvents["shymkent"] {
		t.Errorf("shymkent events = %+v, want %+v", got, regionEvents["shymkent"])
	}
	if got := client.eventsSeen["semey"]; got != (domain.RegionUpdateEventsConfig{}) {
		t.Errorf("semey events = %+v, want zero (path-based fallback)", got)
	}
}

// TestUpdatesUseCaseGetRegionUpdates_ExcludesAstana verifies astanahub is
// skipped entirely (case-insensitively) — it has its own /get_astana_updates
// command, so it must not appear in the region report or be queried here.
func TestUpdatesUseCaseGetRegionUpdates_ExcludesAstana(t *testing.T) {
	client := &fakeUpdatesClient{
		campuses: []string{"AstanaHub", "shymkent"},
		stats: map[string]*domain.RegionUpdatesInfo{
			"AstanaHub": {Region: "AstanaHub"},
			"shymkent":  {Region: "shymkent"},
		},
	}

	report, err := NewUpdatesUseCase(client, nil, nil).GetRegionUpdates(context.Background())
	if err != nil {
		t.Fatalf("GetRegionUpdates returned error: %v", err)
	}

	if len(report.Regions) != 1 || report.Regions[0].Region != "shymkent" {
		t.Fatalf("regions = %+v, want only shymkent", report.Regions)
	}
	for _, c := range client.statsCalls {
		if c == "AstanaHub" {
			t.Errorf("astana must not be queried for region stats, statsCalls = %v", client.statsCalls)
		}
	}
	if len(report.Errors) != 0 {
		t.Errorf("errors = %+v, want none", report.Errors)
	}
}

// TestPiscineRegistrations_DedupesSharedPathAndFiltersRegion verifies that
// several concurrent events sharing one path collapse to a single per-path line
// (path-based counting), that only the region's own paths are counted, and that
// upcoming piscines are flagged.
func TestPiscineRegistrations_DedupesSharedPathAndFiltersRegion(t *testing.T) {
	start655 := time.Date(2026, 7, 1, 5, 0, 0, 0, time.UTC)
	start684 := time.Date(2026, 7, 7, 4, 0, 0, 0, time.UTC)
	startRust := time.Date(2026, 7, 20, 5, 0, 0, 0, time.UTC)

	client := &fakeUpdatesClient{
		current: []domain.PiscineEvent{
			// Two concurrent streams of the same path (ids 655 and 684).
			{ID: 655, Path: "/astanahub/ai-curriculum/prompt-piscine", StartAt: start655},
			{ID: 684, Path: "/astanahub/ai-curriculum/prompt-piscine", StartAt: start684},
			// A different region — must be excluded from astanahub's counts.
			{ID: 900, Path: "/shymkent/module/piscine-go", StartAt: start655},
		},
		upcoming: []domain.PiscineEvent{
			{ID: 665, Path: "/astanahub/module/piscine-rust", StartAt: startRust},
		},
		regByPath: map[string]int{
			"/astanahub/ai-curriculum/prompt-piscine": 42,
			"/astanahub/module/piscine-rust":          17,
			"/shymkent/module/piscine-go":             99,
		},
	}

	uc := NewUpdatesUseCase(client, nil, nil)
	got := uc.piscineRegistrations(
		context.Background(), "astanahub",
		client.current, client.upcoming, time.Now(),
	)

	if len(got) != 2 {
		t.Fatalf("piscineRegistrations len = %d, want 2 (shared path deduped, other region excluded): %+v", len(got), got)
	}

	prompt := got[0]
	if prompt.Path != "/astanahub/ai-curriculum/prompt-piscine" {
		t.Errorf("first path = %q", prompt.Path)
	}
	if prompt.Label != "ai-curriculum/prompt-piscine" {
		t.Errorf("first label = %q, want ai-curriculum/prompt-piscine", prompt.Label)
	}
	if prompt.Count != 42 {
		t.Errorf("first count = %d, want 42", prompt.Count)
	}
	if prompt.Upcoming {
		t.Errorf("current piscine marked upcoming")
	}
	// Earliest start across the shared-path events (655 < 684).
	if !prompt.StartAt.Equal(start655) {
		t.Errorf("first startAt = %v, want %v", prompt.StartAt, start655)
	}

	rust := got[1]
	if rust.Path != "/astanahub/module/piscine-rust" || rust.Count != 17 || !rust.Upcoming {
		t.Errorf("second reg = %+v, want piscine-rust count=17 upcoming=true", rust)
	}

	// The shared path is counted once, not twice; the other region is not counted.
	for _, p := range client.pathCounted {
		if p == "/shymkent/module/piscine-go" {
			t.Errorf("counted a path from another region: %q", p)
		}
	}
}
