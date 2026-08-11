// Package config loads application configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"admin-bot/internal/domain"
)

// Config holds every configurable value for the bot.
type Config struct {
	// Telegram
	TelegramToken string
	ChatIDs       []int64

	// AdminChatIDs is the authorization allowlist for INCOMING commands and
	// callbacks. Defaults to ChatIDs when ADMIN_CHAT_IDS is unset, so the bot
	// only accepts commands from the same chats it broadcasts to.
	AdminChatIDs []int64

	// SuperAdminID is the single user (from SUPER_ADMIN_USER_ID) who receives
	// access requests and presses the approve/reject buttons. Always authorized.
	SuperAdminID int64

	// AdminUserIDs is an optional pre-seed list (ADMIN_USER_IDS): on first start,
	// when the access store is empty, these users are inserted as approved so an
	// existing hand-configured allowlist keeps working.
	AdminUserIDs []int64

	// AccessStorePath is where the JSON access store lives (ACCESS_STORE_PATH).
	AccessStorePath string

	// SubscriptionStorePath is where the JSON announcement-subscription store
	// lives (SUBSCRIPTION_STORE_PATH). It holds "user → piscines + on/off" for
	// /announce and the scheduled announcements.
	SubscriptionStorePath string

	// 01-edu
	OneEduBaseURL     string
	OneEduAccessToken string

	// Apply/leadform site (optional). When both are set, /get_region_updates
	// reports the per-campus application count (заявки с сайта) fetched from
	// APPLY_BASE_URL/api/export.json. Leaving either blank disables the lead line.
	ApplyBaseURL     string
	ApplyAccessToken string

	// Templates
	TemplatesPath string

	// Timezone for cron scheduler (e.g. "Asia/Almaty")
	Timezone string

	// Google Sheets (optional)
	GoogleCredentialsFile string

	// Pre-configured Google Sheets defense tables, indexed by piscine and week.
	SheetIDs  map[domain.PiscineType]map[int]string
	SheetURLs map[domain.PiscineType]map[int]string

	// SheetSlots maps each configured spreadsheet ID to every SHEET_* variable
	// pointing at it (sorted). Two or more names for one ID mean two slots share
	// a single physical document, so writing one silently destroys the other's
	// defense table — the bot warns instead of losing data quietly.
	SheetSlots map[string][]string

	// UniversalSheetID/URL is the shared fallback defense table (SHEET_UNIVERSAL)
	// used for piscines without a dedicated sheet: Piscine RUST and any
	// dynamically discovered ("other") pool.
	UniversalSheetID  string
	UniversalSheetURL string

	// RegionEvents pins the authoritative 01-edu event IDs per region (campus),
	// keyed by lowercased campus name. Populated from the built-in defaults
	// overlaid with REGION_<NAME>_{CHECKIN,PISCINE,MODULE}_EVENT_ID env vars.
	RegionEvents map[string]domain.RegionUpdateEventsConfig
}

// regionEventEnvRe matches REGION_<NAME>_<KIND>_EVENT_ID env vars. <NAME> is
// greedy so region names may themselves contain underscores; the fixed suffix
// disambiguates the kind.
var regionEventEnvRe = regexp.MustCompile(`^REGION_(.+)_(CHECKIN|PISCINE|MODULE)_EVENT_ID$`)

// spreadsheetIDRe extracts the spreadsheet ID from a Google Sheets URL.
var spreadsheetIDRe = regexp.MustCompile(`spreadsheets/d/([a-zA-Z0-9_-]+)`)

var sheetEnvMap = []struct {
	env     string
	piscine domain.PiscineType
	week    int
}{
	{"SHEET_GO_WEEK1", domain.PiscineGo, 1},
	{"SHEET_GO_WEEK2", domain.PiscineGo, 2},
	{"SHEET_GO_WEEK3", domain.PiscineGo, 3},
	{"SHEET_JS_WEEK1", domain.PiscineJS, 1},
	{"SHEET_JS_WEEK2", domain.PiscineJS, 2},
	{"SHEET_JS_WEEK3", domain.PiscineJS, 3},
	// The three parallel AI streams each get their own defense tables, one per
	// week (domain.TotalWeeks for AI == 4). Variable names are consumed by
	// external scripts and .env, so the SHEET_AI<n>_WEEK<w> format is fixed.
	{"SHEET_AI1_WEEK1", domain.PiscineAI_1, 1},
	{"SHEET_AI1_WEEK2", domain.PiscineAI_1, 2},
	{"SHEET_AI1_WEEK3", domain.PiscineAI_1, 3},
	{"SHEET_AI1_WEEK4", domain.PiscineAI_1, 4},
	{"SHEET_AI2_WEEK1", domain.PiscineAI_2, 1},
	{"SHEET_AI2_WEEK2", domain.PiscineAI_2, 2},
	{"SHEET_AI2_WEEK3", domain.PiscineAI_2, 3},
	{"SHEET_AI2_WEEK4", domain.PiscineAI_2, 4},
	{"SHEET_AI3_WEEK1", domain.PiscineAI_3, 1},
	{"SHEET_AI3_WEEK2", domain.PiscineAI_3, 2},
	{"SHEET_AI3_WEEK3", domain.PiscineAI_3, 3},
	{"SHEET_AI3_WEEK4", domain.PiscineAI_3, 4},
}

// Load reads configuration from the environment.
// Load reads configuration from the environment.
func Load() (*Config, error) {
	token, err := requireEnv("TELEGRAM_TOKEN")
	if err != nil {
		return nil, err
	}

	eduURL, err := requireEnv("ONEEDU_BASE_URL")
	if err != nil {
		return nil, err
	}
	eduURL = ensureScheme(eduURL)

	// SECURITY: refuse cleartext HTTP for the upstream that carries the access
	// token and all GraphQL traffic, unless an explicit opt-out is set (local dev).
	if strings.HasPrefix(eduURL, "http://") && os.Getenv("ONEEDU_ALLOW_INSECURE") != "1" {
		return nil, fmt.Errorf("ONEEDU_BASE_URL must use https:// (set ONEEDU_ALLOW_INSECURE=1 to override for local dev)")
	}

	eduToken, err := requireEnv("PLATFORM_ACCESS_TOKEN")
	if err != nil {
		return nil, err
	}

	// Apply/leadform site is optional. Both the URL and the token must be present
	// for the lead line to be enabled; if a URL is given it must be https:// (the
	// same insecure opt-out as the platform token) since it carries a bearer token.
	applyURL := strings.TrimSpace(os.Getenv("APPLY_BASE_URL"))
	if applyURL != "" {
		applyURL = ensureScheme(applyURL)
		if strings.HasPrefix(applyURL, "http://") && os.Getenv("APPLY_ALLOW_INSECURE") != "1" {
			return nil, fmt.Errorf("APPLY_BASE_URL must use https:// (set APPLY_ALLOW_INSECURE=1 to override for local dev)")
		}
	}
	applyToken := strings.TrimSpace(os.Getenv("APPLY_ACCESS_TOKEN"))

	chatIDs, err := parseChatIDs(os.Getenv("CHAT_IDS"))
	if err != nil {
		return nil, fmt.Errorf("CHAT_IDS: %w", err)
	}
	// CHAT_IDS is required: with no chats the scheduler broadcasts to nobody and
	// (since AdminChatIDs falls back to it) every command is rejected — a deploy
	// that looks healthy but is completely inert. Fail loudly at startup instead.
	if len(chatIDs) == 0 {
		return nil, fmt.Errorf("CHAT_IDS is required: provide a comma-separated list of chat IDs")
	}

	// Authorization allowlist for incoming commands. Falls back to CHAT_IDS.
	adminIDs, err := parseChatIDs(os.Getenv("ADMIN_CHAT_IDS"))
	if err != nil {
		return nil, fmt.Errorf("ADMIN_CHAT_IDS: %w", err)
	}
	if len(adminIDs) == 0 {
		adminIDs = chatIDs
	}

	// SUPER_ADMIN_USER_ID is required: it is the only user who can approve or
	// reject access requests, so without it the request workflow is inert.
	superAdminRaw, err := requireEnv("SUPER_ADMIN_USER_ID")
	if err != nil {
		return nil, err
	}
	superAdminID, err := strconv.ParseInt(strings.TrimSpace(superAdminRaw), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("SUPER_ADMIN_USER_ID: invalid user ID %q: %w", superAdminRaw, err)
	}

	adminUserIDs, err := parseChatIDs(os.Getenv("ADMIN_USER_IDS"))
	if err != nil {
		return nil, fmt.Errorf("ADMIN_USER_IDS: %w", err)
	}

	sheetIDs, sheetURLs, sheetSlots := loadSheetMaps()
	universalURL := strings.TrimSpace(os.Getenv("SHEET_UNIVERSAL"))
	universalID := parseSpreadsheetID(universalURL)
	if universalID != "" {
		sheetSlots[universalID] = append(sheetSlots[universalID], "SHEET_UNIVERSAL")
	}
	for id := range sheetSlots {
		sort.Strings(sheetSlots[id])
	}

	regionEvents, err := loadRegionEvents()
	if err != nil {
		return nil, err
	}

	return &Config{
		TelegramToken:         token,
		ChatIDs:               chatIDs,
		AdminChatIDs:          adminIDs,
		SuperAdminID:          superAdminID,
		AdminUserIDs:          adminUserIDs,
		AccessStorePath:       envOr("ACCESS_STORE_PATH", "data/access.json"),
		SubscriptionStorePath: envOr("SUBSCRIPTION_STORE_PATH", "data/subscriptions.json"),
		OneEduBaseURL:         eduURL,
		OneEduAccessToken:     eduToken,
		ApplyBaseURL:          applyURL,
		ApplyAccessToken:      applyToken,
		TemplatesPath:         envOr("TEMPLATES_PATH", "messages"),
		Timezone:              envOr("TIMEZONE", "Asia/Almaty"),
		GoogleCredentialsFile: os.Getenv("GOOGLE_CREDENTIALS_FILE"),
		SheetIDs:              sheetIDs,
		SheetURLs:             sheetURLs,
		SheetSlots:            sheetSlots,
		UniversalSheetID:      universalID,
		UniversalSheetURL:     universalURL,
		RegionEvents:          regionEvents,
	}, nil
}

// loadRegionEvents starts from the built-in per-region defaults and overlays any
// REGION_<NAME>_{CHECKIN,PISCINE,MODULE}_EVENT_ID env vars, merging field by
// field so an override for one metric leaves the region's other metrics on their
// default. Region names are lowercased to match campus names from the platform.
func loadRegionEvents() (map[string]domain.RegionUpdateEventsConfig, error) {
	out := domain.DefaultRegionUpdateEvents()

	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key, raw := kv[:eq], strings.TrimSpace(kv[eq+1:])
		m := regionEventEnvRe.FindStringSubmatch(key)
		if m == nil || raw == "" {
			continue
		}

		id, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid event ID %q: %w", key, raw, err)
		}
		if id <= 0 {
			return nil, fmt.Errorf("%s: event ID must be positive, got %d", key, id)
		}

		region := strings.ToLower(m[1])
		cfg := out[region]
		switch m[2] {
		case "CHECKIN":
			cfg.CheckinEventID = id
		case "PISCINE":
			cfg.PiscineEventID = id
		case "MODULE":
			cfg.ModuleEventID = id
		}
		out[region] = cfg
	}

	return out, nil
}

func loadSheetMaps() (
	ids map[domain.PiscineType]map[int]string,
	urls map[domain.PiscineType]map[int]string,
	slots map[string][]string,
) {
	ids = make(map[domain.PiscineType]map[int]string)
	urls = make(map[domain.PiscineType]map[int]string)
	slots = make(map[string][]string)

	for _, e := range sheetEnvMap {
		raw := strings.TrimSpace(os.Getenv(e.env))
		spreadsheetID := parseSpreadsheetID(raw)
		if spreadsheetID == "" {
			continue
		}

		if _, ok := ids[e.piscine]; !ok {
			ids[e.piscine] = make(map[int]string)
		}
		if _, ok := urls[e.piscine]; !ok {
			urls[e.piscine] = make(map[int]string)
		}
		ids[e.piscine][e.week] = spreadsheetID
		urls[e.piscine][e.week] = raw
		slots[spreadsheetID] = append(slots[spreadsheetID], e.env)
	}
	return ids, urls, slots
}

// DuplicateSheetSlots returns the spreadsheet IDs configured in more than one
// SHEET_* slot, mapped to the names of those slots. Each entry is a pair of
// weeks (or piscines) whose defense tables live in the same document and
// therefore overwrite each other.
func (c *Config) DuplicateSheetSlots() map[string][]string {
	dups := make(map[string][]string)
	for id, names := range c.SheetSlots {
		if len(names) > 1 {
			dups[id] = names
		}
	}
	return dups
}

// parseSpreadsheetID extracts the spreadsheet ID from a full Google Sheets edit
// URL. Returns "" when raw is empty or not a recognizable sheets URL.
func parseSpreadsheetID(raw string) string {
	if raw == "" {
		return ""
	}
	m := spreadsheetIDRe.FindStringSubmatch(raw)
	if len(m) < 2 || m[1] == "" {
		return ""
	}
	return m[1]
}

func requireEnv(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func ensureScheme(url string) string {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}
	return "https://" + url
}

func parseChatIDs(raw string) ([]int64, error) {
	if raw == "" {
		return nil, nil
	}
	var ids []int64
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid chat ID %q: %w", s, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
