package sheets

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"

	"admin-bot/internal/usecase"
)

// Client wraps the Google Sheets API.
//
// The Drive API is no longer needed: tables are pre-created and shared by the
// administrator, so the bot only writes data, never creates files or changes
// permissions.
type Client struct {
	sheetsSvc *sheets.Service
	logger    *slog.Logger
}

// Layout constants for the generated defense table.
//
// Note: the slot/break durations mirror the scheduling assumptions in
// internal/usecase/defense.go. The start hour and column count are no longer
// fixed here — they travel in usecase.DefenseSchedule so /edit_tables can vary
// them per table.
const (
	// slotDuration is only a fallback for schedules that don't carry an explicit
	// slot length (SlotMinutes == 0). Normal paths set it via the schedule.
	slotDuration = 30 * time.Minute

	// maxResetRows/maxResetColumns bound the range wiped before rewriting (the
	// old A1:Z1000). The actual range is clamped to the sheet's real grid, since
	// a range past the grid edge is rejected by the API.
	maxResetRows    = 1000
	maxResetColumns = 26
)

// NewClient creates a Sheets client using a service account credentials JSON file.
func NewClient(credentialsFile string, logger *slog.Logger) (*Client, error) {
	ctx := context.Background()

	sheetsSvc, err := sheets.NewService(ctx, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("create sheets service: %w", err)
	}

	return &Client{
		sheetsSvc: sheetsSvc,
		logger:    logger,
	}, nil
}

// DefenseTableParams holds everything needed to (re)populate a defense table.
type DefenseTableParams struct {
	RaidName    string
	DefenseDate time.Time // Monday date for the defense
	Schedule    usecase.DefenseSchedule
}

// UpdateResult reports the outcome of a table update.
type UpdateResult struct {
	// URL is the canonical link to the spreadsheet.
	URL string

	// FormatFailed is true when the rows were written but formatting could not be
	// applied. The data is usable, so this is not an error — but it is surfaced
	// rather than only logged, because the visible symptom ("the table looks
	// broken") otherwise has no explanation in the chat.
	FormatFailed bool
}

// UpdateDefenseTable wipes the first sheet of the given spreadsheet and
// rewrites it with the latest defense schedule. The spreadsheet must already
// exist and be shared with the bot's service account.
func (c *Client) UpdateDefenseTable(ctx context.Context, spreadsheetID string, params DefenseTableParams) (UpdateResult, error) {
	// 1. Resolve the first sheet (its tab name, sheetId and grid size).
	meta, err := c.firstSheetMeta(ctx, spreadsheetID)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("inspect spreadsheet: %w", err)
	}

	rowData := buildRows(params)

	// 2. Reset the working range — both values and formatting — so nothing from a
	// previous, differently sized table bleeds into the new one.
	if err := c.resetSheet(ctx, spreadsheetID, meta); err != nil {
		return UpdateResult{}, fmt.Errorf("reset sheet: %w", err)
	}

	// 3. Write the new content.
	if err := c.populateSheet(ctx, spreadsheetID, meta.title, params, rowData); err != nil {
		return UpdateResult{}, fmt.Errorf("populate sheet: %w", err)
	}

	// 4. Apply formatting. The rows are already in place, so a failure here does
	// not invalidate the table — report it instead of failing the whole update.
	result := UpdateResult{URL: fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s/edit", spreadsheetID)}
	if err := c.formatSheet(ctx, spreadsheetID, meta.sheetID, rowData, params.Schedule.Columns); err != nil {
		c.logger.Warn("formatting failed (data written)", "spreadsheet_id", spreadsheetID, "err", err)
		result.FormatFailed = true
	}

	c.logger.Info("defense table updated", "spreadsheet_id", spreadsheetID,
		"url", result.URL, "rows", len(rowData), "format_failed", result.FormatFailed)
	return result, nil
}

// sheetMeta describes the tab the bot writes to. The grid size matters because
// the reset below addresses cells by index, and a range past the grid edge is
// rejected by the API.
type sheetMeta struct {
	title       string
	sheetID     int64
	rowCount    int64
	columnCount int64
}

// firstSheetMeta returns the first sheet of the spreadsheet — writes and
// formatting both have to target a specific tab, and the pre-created templates
// have a single one.
func (c *Client) firstSheetMeta(ctx context.Context, spreadsheetID string) (sheetMeta, error) {
	resp, err := c.sheetsSvc.Spreadsheets.Get(spreadsheetID).
		Fields("sheets.properties.sheetId,sheets.properties.title,sheets.properties.gridProperties").
		Context(ctx).Do()
	if err != nil {
		return sheetMeta{}, err
	}
	if len(resp.Sheets) == 0 || resp.Sheets[0].Properties == nil {
		return sheetMeta{}, fmt.Errorf("spreadsheet %q has no sheets", spreadsheetID)
	}

	props := resp.Sheets[0].Properties
	meta := sheetMeta{title: props.Title, sheetID: props.SheetId}
	if props.GridProperties != nil {
		meta.rowCount = props.GridProperties.RowCount
		meta.columnCount = props.GridProperties.ColumnCount
	}
	return meta, nil
}
