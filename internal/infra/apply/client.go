// Package apply is a thin client for the public apply/leadform site's export
// API (apply.tomorrow-school.ai), used to report the number of applications
// (заявки) submitted per campus — all-time, today and yesterday.
package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"admin-bot/internal/domain"
)

const (
	httpTimeout = 30 * time.Second

	// exportPath is the JSON export endpoint that returns per-campus totals and
	// the individual leads (with timestamps) used for the daily breakdown.
	exportPath = "/api/export.json"

	// maxResponseBytes caps how much of an upstream response we read into memory,
	// guarding against a malicious or malfunctioning endpoint causing OOM. The
	// export payload embeds every lead, so it is allowed to be larger than the
	// small aggregate responses elsewhere.
	maxResponseBytes = 16 << 20 // 16 MiB
)

// Client fetches lead counts from the apply/leadform export endpoint.
type Client struct {
	httpClient  *http.Client
	baseURL     string
	accessToken string
	loc         *time.Location
	logger      *slog.Logger
}

// NewClient builds a lead client for baseURL, authenticating with accessToken
// as a Bearer token. baseURL is the site origin (e.g.
// "https://apply.tomorrow-school.ai"); the export path is appended internally.
// loc is the timezone used to bucket leads into "today"/"yesterday"; a nil loc
// falls back to UTC.
func NewClient(baseURL, accessToken string, loc *time.Location, logger *slog.Logger) *Client {
	if loc == nil {
		loc = time.UTC
	}
	return &Client{
		httpClient:  &http.Client{Timeout: httpTimeout},
		baseURL:     strings.TrimRight(baseURL, "/"),
		accessToken: accessToken,
		loc:         loc,
		logger:      logger,
	}
}

// exportResponse is the subset of /api/export.json we consume: the per-campus
// aggregate totals (authoritative all-time counts) and the individual leads
// (used for the today/yesterday breakdown). The full payload carries more
// (names, per-status breakdowns); we deliberately decode only what we need.
type exportResponse struct {
	ByCampus []struct {
		CampusID string `json:"campus_id"`
		Total    int    `json:"total"`
	} `json:"by_campus"`
	Leads []struct {
		CampusID  string    `json:"campus_id"`
		CreatedAt time.Time `json:"created_at"`
	} `json:"leads"`
}

// GetLeadCountsByCampus fetches the export payload and returns per-campus counts
// (total plus today's and yesterday's submissions), keyed by lowercased campus
// ID so lookups are case-insensitive.
func (c *Client) GetLeadCountsByCampus(ctx context.Context) (map[string]domain.LeadCounts, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+exportPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apply export request: %w", c.scrub(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("apply export: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("apply export returned status %d", resp.StatusCode)
	}

	var export exportResponse
	if err := json.Unmarshal(raw, &export); err != nil {
		return nil, fmt.Errorf("apply export: decode: %w", err)
	}

	return aggregateByCampus(export, time.Now(), c.loc), nil
}

// aggregateByCampus rolls the export payload up into per-campus counts. The
// all-time Total is taken from by_campus (the API's own authoritative aggregate,
// robust even if the leads array were ever truncated); Today and Yesterday are
// counted from the individual leads, bucketed by day boundaries in loc. Pure —
// now and loc are injected — so the date arithmetic is unit-testable.
func aggregateByCampus(export exportResponse, now time.Time, loc *time.Location) map[string]domain.LeadCounts {
	if loc == nil {
		loc = time.UTC
	}
	todayStart := startOfDay(now, loc)
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	tomorrowStart := todayStart.AddDate(0, 0, 1)

	out := make(map[string]domain.LeadCounts, len(export.ByCampus))

	for _, campus := range export.ByCampus {
		id := strings.ToLower(strings.TrimSpace(campus.CampusID))
		if id == "" {
			continue
		}
		lc := out[id]
		lc.Total = campus.Total
		out[id] = lc
	}

	for _, lead := range export.Leads {
		id := strings.ToLower(strings.TrimSpace(lead.CampusID))
		if id == "" {
			continue
		}
		t := lead.CreatedAt.In(loc)
		lc := out[id]
		switch {
		case !t.Before(todayStart) && t.Before(tomorrowStart):
			lc.Today++
		case !t.Before(yesterdayStart) && t.Before(todayStart):
			lc.Yesterday++
		}
		out[id] = lc
	}

	return out
}

// startOfDay returns midnight in loc for the day containing t.
func startOfDay(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// scrub redacts the access token from an error before it is logged or returned.
// The token rides in a request header (never the URL), so net/http won't embed
// it in the *url.Error on transport failure — this is defense in depth.
func (c *Client) scrub(err error) error {
	if err == nil || c.accessToken == "" {
		return err
	}
	if s := err.Error(); strings.Contains(s, c.accessToken) {
		return errors.New(strings.ReplaceAll(s, c.accessToken, "[REDACTED_ACCESS_TOKEN]"))
	}
	return err
}
