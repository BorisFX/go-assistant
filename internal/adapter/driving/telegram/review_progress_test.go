package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/legalreview"
)

func TestRenderProgressDigestShowsCountAndEta(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	out := renderProgress("testHotel", legalreview.ProgressEvent{
		Stage: legalreview.StageDigest, Done: 10, Total: 40, Detail: "Том 6 ПОС.pdf",
	}, start, start)

	for _, want := range []string{"testHotel", "10 из 40", "Том 6 ПОС.pdf"} {
		if !strings.Contains(out, want) {
			t.Errorf("progress lacks %q:\n%s", want, out)
		}
	}
	// Ten of forty in ten minutes leaves thirty minutes.
	if !strings.Contains(out, "30 минут") {
		t.Errorf("eta missing or wrong:\n%s", out)
	}
}

// Before the first document lands there is nothing to extrapolate from.
func TestRenderProgressNoEtaBeforeFirstDocument(t *testing.T) {
	now := time.Now()
	out := renderProgress("X", legalreview.ProgressEvent{
		Stage: legalreview.StageDigest, Done: 0, Total: 40,
	}, now, now)
	if strings.Contains(out, "осталось") {
		t.Errorf("no estimate is possible yet:\n%s", out)
	}
	if !strings.Contains(out, "0 из 40") {
		t.Errorf("counter missing:\n%s", out)
	}
}

func TestRenderProgressCoordinatorShowsElapsed(t *testing.T) {
	start := time.Now().Add(-20 * time.Minute)
	stage := time.Now().Add(-3 * time.Minute)
	out := renderProgress("X", legalreview.ProgressEvent{
		Stage: legalreview.StageCoordinator, Done: 39, Total: 39,
	}, start, stage)

	if !strings.Contains(out, "заключение") {
		t.Errorf("stage name missing:\n%s", out)
	}
	if !strings.Contains(out, "3 минуты") {
		t.Errorf("elapsed in stage missing:\n%s", out)
	}
	if !strings.Contains(out, "39") {
		t.Errorf("document count missing:\n%s", out)
	}
}

func TestPluralMinutes(t *testing.T) {
	cases := map[int]string{1: "1 минута", 2: "2 минуты", 5: "5 минут", 11: "11 минут", 21: "21 минута", 44: "44 минуты", 0: "меньше минуты"}
	for n, want := range cases {
		if got := minutesRU(n); got != want {
			t.Errorf("minutesRU(%d) = %q, want %q", n, got, want)
		}
	}
}
