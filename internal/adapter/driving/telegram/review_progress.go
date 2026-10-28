package telegram

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/legalreview"
)

// progressTick is how often the message is refreshed while a stage runs without
// emitting events of its own — the coordinator can think for five minutes.
const progressTick = 30 * time.Second

// stageTitles are what the user reads. They name the work, not the machinery.
var stageTitles = map[legalreview.Stage]string{
	legalreview.StageCollect:     "собираю документы из папки",
	legalreview.StageDigest:      "читаю документы",
	legalreview.StageCrossCheck:  "сверяю числа между документами",
	legalreview.StageCoordinator: "составляю заключение",
	legalreview.StageReport:      "собираю отчёт и PDF",
}

// reviewProgress keeps one Telegram message up to date for the whole run. A
// review takes the better part of an hour, and a silent chat is
// indistinguishable from a hung bot.
type reviewProgress struct {
	stream *DraftStream
	folder string

	mu        sync.Mutex
	started   time.Time
	stageAt   time.Time
	last      legalreview.ProgressEvent
	stop      chan struct{}
	stopped   bool
	lastPaint string
}

func newReviewProgress(stream *DraftStream, folder string) *reviewProgress {
	now := time.Now()
	p := &reviewProgress{
		stream:  stream,
		folder:  folder,
		started: now,
		stageAt: now,
		last:    legalreview.ProgressEvent{Stage: legalreview.StageCollect},
		stop:    make(chan struct{}),
	}
	p.paint()
	go p.loop()
	return p
}

// loop refreshes the elapsed time so the message keeps moving during stages
// that report nothing for minutes.
func (p *reviewProgress) loop() {
	t := time.NewTicker(progressTick)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.paint()
		}
	}
}

// Handle is the legalreview.Progress callback; it is called from several
// goroutines at once.
func (p *reviewProgress) Handle(e legalreview.ProgressEvent) {
	p.mu.Lock()
	if e.Stage != p.last.Stage {
		p.stageAt = time.Now()
	}
	// Events arrive out of order across workers; never walk the counter back.
	if e.Stage == p.last.Stage && e.Done < p.last.Done {
		e.Done = p.last.Done
	}
	p.last = e
	p.mu.Unlock()
	p.paint()
}

// Done stops the refresher. The finished report replaces the message.
func (p *reviewProgress) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.stopped {
		p.stopped = true
		close(p.stop)
	}
}

func (p *reviewProgress) paint() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	text := renderProgress(p.folder, p.last, p.started, p.stageAt)
	if text == p.lastPaint {
		p.mu.Unlock()
		return
	}
	p.lastPaint = text
	p.mu.Unlock()

	if p.stream.messageID == 0 {
		_ = p.stream.SendDraft(text)
		return
	}
	_ = p.stream.UpdateDraft(text)
}

// renderProgress writes the status the user sees.
func renderProgress(folder string, e legalreview.ProgressEvent, started, stageAt time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Разбираю папку «%s»\n\n", folder)

	title := stageTitles[e.Stage]
	if title == "" {
		title = "работаю"
	}
	fmt.Fprintf(&b, "Сейчас: %s\n", title)

	if e.Stage == legalreview.StageDigest && e.Total > 0 {
		fmt.Fprintf(&b, "Готово: %d из %d\n", e.Done, e.Total)
		if e.Done > 0 {
			perDoc := time.Since(stageAt) / time.Duration(e.Done)
			left := perDoc * time.Duration(e.Total-e.Done)
			if left > 0 {
				fmt.Fprintf(&b, "Осталось примерно %s\n", minutesRU(int(left.Minutes())))
			}
		}
		if e.Detail != "" {
			fmt.Fprintf(&b, "Последний: %s\n", e.Detail)
		}
	}

	if e.Stage == legalreview.StageCoordinator {
		fmt.Fprintf(&b, "Документов в работе: %d\n", e.Done)
		fmt.Fprintf(&b, "Этап идёт %s, обычно занимает 4–6 минут\n", minutesRU(int(time.Since(stageAt).Minutes())))
	}

	fmt.Fprintf(&b, "\nВсего прошло: %s", minutesRU(int(time.Since(started).Minutes())))
	return b.String()
}

// minutesRU writes a minute count the way Russian needs it.
func minutesRU(n int) string {
	if n <= 0 {
		return "меньше минуты"
	}
	word := "минут"
	switch {
	case n%100 >= 11 && n%100 <= 14:
	case n%10 == 1:
		word = "минута"
	case n%10 >= 2 && n%10 <= 4:
		word = "минуты"
	}
	return fmt.Sprintf("%d %s", n, word)
}
