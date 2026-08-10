package sheets

import (
	"context"

	"google.golang.org/api/sheets/v4"
)

// defaultColumnWidth is the Google Sheets default column width in pixels. The
// reset restores it so columns of a previous, wider table don't stay stretched.
const defaultColumnWidth = 100

// resetSheet clears the working range before the new table is written: cell
// VALUES and cell FORMAT (fill, borders, alignment), plus column widths.
//
// Clearing only values (Spreadsheets.Values.Clear, as this used to do) left the
// formatting behind, and formatSheet only ever paints the rows/columns the NEW
// table occupies. So after a big table (say 10 rows with two orange break rows)
// was replaced by a small one (3 rows, no breaks), the old fills and borders
// stayed on the rows below — the "styles look broken" symptom.
//
// The range is clamped to the sheet's real grid: a GridRange past the grid edge
// is rejected by the API.
func (c *Client) resetSheet(ctx context.Context, spreadsheetID string, meta sheetMeta) error {
	rows, cols := resetRange(meta)
	if rows <= 0 || cols <= 0 {
		// No grid information (or an empty grid): nothing addressable to reset.
		return nil
	}

	_, err := c.sheetsSvc.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			clearCellsRequest(meta.sheetID, rows, cols),
			columnWidthRequest(meta.sheetID, 0, cols, defaultColumnWidth),
		},
	}).Context(ctx).Do()
	return err
}

// resetRange is the range to wipe: the intended A1:Z1000 window, clamped to the
// sheet's actual grid so the request is never out of bounds. Zero means the
// sheet reported no usable grid, and the caller skips the reset.
func resetRange(meta sheetMeta) (rows, cols int64) {
	return min(meta.rowCount, maxResetRows), min(meta.columnCount, maxResetColumns)
}

// clearCellsRequest empties both the values and the formatting of a range.
// Passing an empty CellData with these Fields is how the Sheets API expresses
// "unset these properties".
func clearCellsRequest(sheetID, rows, cols int64) *sheets.Request {
	return &sheets.Request{
		RepeatCell: &sheets.RepeatCellRequest{
			Range: &sheets.GridRange{
				SheetId:          sheetID,
				StartRowIndex:    0,
				EndRowIndex:      rows,
				StartColumnIndex: 0,
				EndColumnIndex:   cols,
			},
			Cell:   &sheets.CellData{},
			Fields: "userEnteredValue,userEnteredFormat",
		},
	}
}

// formatSheet applies basic formatting: bold header, colored break rows,
// column widths, and borders. Each formatting concern lives in its own helper;
// this function just orchestrates the BatchUpdate.
func (c *Client) formatSheet(ctx context.Context, spreadsheetID string, sheetID int64, rows []tableRow, groupColumns int) error {
	if groupColumns < 1 {
		groupColumns = 1
	}
	totalColumns := int64(groupColumns + 1) // +1 for the time column

	requests := []*sheets.Request{boldHeaderRequest(sheetID)}
	requests = append(requests, breakRowRequests(sheetID, rows)...)
	requests = append(requests, columnWidthRequests(sheetID, totalColumns)...)
	requests = append(requests, bordersRequest(sheetID, int64(len(rows)+1), totalColumns))

	_, err := c.sheetsSvc.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: requests,
	}).Context(ctx).Do()
	return err
}

// boldHeaderRequest formats row 0 (the header) as bold with a light-blue fill.
func boldHeaderRequest(sheetID int64) *sheets.Request {
	return &sheets.Request{
		RepeatCell: &sheets.RepeatCellRequest{
			Range: &sheets.GridRange{
				SheetId:       sheetID,
				StartRowIndex: 0,
				EndRowIndex:   1,
			},
			Cell: &sheets.CellData{
				UserEnteredFormat: &sheets.CellFormat{
					TextFormat:      &sheets.TextFormat{Bold: true},
					BackgroundColor: &sheets.Color{Red: 0.85, Green: 0.92, Blue: 1.0, Alpha: 1.0},
				},
			},
			Fields: "userEnteredFormat(textFormat,backgroundColor)",
		},
	}
}

// breakRowRequests highlights each "Перерыв" row with a warm fill and centers
// the text. Returns nil if there are no break rows.
func breakRowRequests(sheetID int64, rows []tableRow) []*sheets.Request {
	var reqs []*sheets.Request
	for i, row := range rows {
		if row.Type != rowBreak {
			continue
		}
		rowIndex := int64(i + 1) // +1 because row 0 is the header
		reqs = append(reqs, &sheets.Request{
			RepeatCell: &sheets.RepeatCellRequest{
				Range: &sheets.GridRange{
					SheetId:       sheetID,
					StartRowIndex: rowIndex,
					EndRowIndex:   rowIndex + 1,
				},
				Cell: &sheets.CellData{
					UserEnteredFormat: &sheets.CellFormat{
						BackgroundColor:     &sheets.Color{Red: 1.0, Green: 0.95, Blue: 0.8, Alpha: 1.0},
						HorizontalAlignment: "CENTER",
					},
				},
				Fields: "userEnteredFormat(backgroundColor,horizontalAlignment)",
			},
		})
	}
	return reqs
}

// columnWidthRequests sets column A (time) to 80px and the group columns to
// 200px each.
func columnWidthRequests(sheetID, totalColumns int64) []*sheets.Request {
	return []*sheets.Request{
		columnWidthRequest(sheetID, 0, 1, 80),
		columnWidthRequest(sheetID, 1, totalColumns, 200),
	}
}

func columnWidthRequest(sheetID, start, end, pixelSize int64) *sheets.Request {
	return &sheets.Request{
		UpdateDimensionProperties: &sheets.UpdateDimensionPropertiesRequest{
			Range: &sheets.DimensionRange{
				SheetId:    sheetID,
				Dimension:  "COLUMNS",
				StartIndex: start,
				EndIndex:   end,
			},
			Properties: &sheets.DimensionProperties{PixelSize: pixelSize},
			Fields:     "pixelSize",
		},
	}
}

// bordersRequest adds a light-gray solid border around and between every cell
// in the populated range (header + data rows × totalColumns).
func bordersRequest(sheetID, totalRows, totalColumns int64) *sheets.Request {
	border := &sheets.Border{
		Style: "SOLID",
		Color: &sheets.Color{Red: 0.8, Green: 0.8, Blue: 0.8, Alpha: 1.0},
	}
	return &sheets.Request{
		UpdateBorders: &sheets.UpdateBordersRequest{
			Range: &sheets.GridRange{
				SheetId:          sheetID,
				StartRowIndex:    0,
				EndRowIndex:      totalRows,
				StartColumnIndex: 0,
				EndColumnIndex:   totalColumns,
			},
			Top:             border,
			Bottom:          border,
			Left:            border,
			Right:           border,
			InnerVertical:   border,
			InnerHorizontal: border,
		},
	}
}
