package google

import (
	"context"
	"fmt"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// Sheets wraps the Sheets v4 service, pinned to one spreadsheet. It is a plain
// transport: interpreting rows as projects belongs to the app layer.
type Sheets struct {
	svc *sheets.Service
	id  string
}

func NewSheets(ctx context.Context, spreadsheetID string, opts ...option.ClientOption) (*Sheets, error) {
	if spreadsheetID == "" {
		return nil, fmt.Errorf("sheets: spreadsheet id is required")
	}
	svc, err := sheets.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("sheets service: %w", err)
	}
	return &Sheets{svc: svc, id: spreadsheetID}, nil
}

// ReadRange returns the values of an A1 range as strings, padded to a uniform
// width. Sheets omits trailing empty cells, so a row whose last column is blank
// comes back short; padding here keeps callers from repeating bounds checks.
func (s *Sheets) ReadRange(ctx context.Context, a1 string) ([][]string, error) {
	resp, err := s.svc.Spreadsheets.Values.Get(s.id, a1).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("sheets read %s: %w", a1, err)
	}

	width := 0
	for _, row := range resp.Values {
		if len(row) > width {
			width = len(row)
		}
	}

	out := make([][]string, 0, len(resp.Values))
	for _, row := range resp.Values {
		cells := make([]string, width)
		for i, v := range row {
			if v != nil {
				cells[i] = fmt.Sprint(v)
			}
		}
		out = append(out, cells)
	}
	return out, nil
}

func toAnyRow(row []string) []any {
	out := make([]any, len(row))
	for i, v := range row {
		out[i] = v
	}
	return out
}

// AppendRow adds a row after the last non-empty row of the sheet.
func (s *Sheets) AppendRow(ctx context.Context, sheet string, row []string) error {
	if sheet == "" {
		return fmt.Errorf("sheets append: sheet name is required")
	}
	body := &sheets.ValueRange{Values: [][]any{toAnyRow(row)}}
	_, err := s.svc.Spreadsheets.Values.
		Append(s.id, sheet+"!A:A", body).
		ValueInputOption("USER_ENTERED").
		InsertDataOption("INSERT_ROWS").
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("sheets append to %s: %w", sheet, err)
	}
	return nil
}

// UpdateCell writes a single cell. USER_ENTERED makes dates and links behave as
// if a person had typed them, which keeps the registry readable by hand.
func (s *Sheets) UpdateCell(ctx context.Context, a1, value string) error {
	body := &sheets.ValueRange{Values: [][]any{{value}}}
	_, err := s.svc.Spreadsheets.Values.
		Update(s.id, a1, body).
		ValueInputOption("USER_ENTERED").
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("sheets update %s: %w", a1, err)
	}
	return nil
}
