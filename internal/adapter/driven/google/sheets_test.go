package google_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"google.golang.org/api/option"
)

type sheetsStub struct {
	*httptest.Server
	body     string
	lastPath string
	lastBody []byte
}

func newSheetsStub(t *testing.T, body string) *sheetsStub {
	t.Helper()
	s := &sheetsStub{body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastPath = r.URL.Path
		s.lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func newTestSheets(t *testing.T, stub *sheetsStub) *gworkspace.Sheets {
	t.Helper()
	s, err := gworkspace.NewSheets(context.Background(), "1SHEET",
		option.WithoutAuthentication(),
		option.WithEndpoint(stub.URL+"/"),
	)
	if err != nil {
		t.Fatalf("new sheets: %v", err)
	}
	return s
}

func TestSheetsRequiresSpreadsheetID(t *testing.T) {
	if _, err := gworkspace.NewSheets(context.Background(), "", option.WithoutAuthentication()); err == nil {
		t.Fatal("expected an error when spreadsheet id is empty")
	}
}

func TestSheetsReadRange(t *testing.T) {
	stub := newSheetsStub(t, `{"values":[["Проект","Этап"],["Ленина_42","РНС"]]}`)

	rows, err := newTestSheets(t, stub).ReadRange(context.Background(), "Проекты!A:H")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[1][0] != "Ленина_42" {
		t.Errorf("cell: got %q", rows[1][0])
	}
}

// Sheets drops trailing empty cells, so a row with a blank last column comes
// back short. Padding here keeps every caller from repeating bounds checks.
func TestSheetsReadRangePadsShortRows(t *testing.T) {
	stub := newSheetsStub(t, `{"values":[["a","b","c"],["d"]]}`)

	rows, err := newTestSheets(t, stub).ReadRange(context.Background(), "Проекты!A:C")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows[1]) != 3 {
		t.Fatalf("expected the short row padded to 3 cells, got %d", len(rows[1]))
	}
	if rows[1][2] != "" {
		t.Errorf("padded cell must be empty, got %q", rows[1][2])
	}
}

func TestSheetsReadRangeEmptySheet(t *testing.T) {
	stub := newSheetsStub(t, `{}`)

	rows, err := newTestSheets(t, stub).ReadRange(context.Background(), "Проекты!A:H")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no rows, got %d", len(rows))
	}
}

func TestSheetsAppendRowSendsValues(t *testing.T) {
	stub := newSheetsStub(t, `{}`)

	if err := newTestSheets(t, stub).AppendRow(context.Background(), "Проекты", []string{"Мира_7", "ЗОС"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	var sent struct {
		Values [][]any `json:"values"`
	}
	if err := json.Unmarshal(stub.lastBody, &sent); err != nil {
		t.Fatalf("request body is not valid json: %v", err)
	}
	if len(sent.Values) != 1 || len(sent.Values[0]) != 2 {
		t.Fatalf("expected one row of two cells, got %v", sent.Values)
	}
	if sent.Values[0][0] != "Мира_7" {
		t.Errorf("first cell: got %v", sent.Values[0][0])
	}
}

func TestSheetsUpdateCell(t *testing.T) {
	stub := newSheetsStub(t, `{}`)

	if err := newTestSheets(t, stub).UpdateCell(context.Background(), "Проекты!C2", "Ввод"); err != nil {
		t.Fatalf("update: %v", err)
	}

	var sent struct {
		Values [][]any `json:"values"`
	}
	if err := json.Unmarshal(stub.lastBody, &sent); err != nil {
		t.Fatalf("request body is not valid json: %v", err)
	}
	if len(sent.Values) != 1 || sent.Values[0][0] != "Ввод" {
		t.Errorf("expected the new value in the body, got %v", sent.Values)
	}
}
