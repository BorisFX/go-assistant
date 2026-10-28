package legalreview

// Stage names the phase of a review run. A batch of forty documents takes the
// better part of an hour, and without a stage the user cannot tell work from a
// hang.
type Stage string

const (
	StageCollect     Stage = "collect"
	StageDigest      Stage = "digest"
	StageCrossCheck  Stage = "crosscheck"
	StageCoordinator Stage = "coordinator"
	StageReport      Stage = "report"
)

// ProgressEvent is one report from inside a run. Done and Total are meaningful
// only for stages that count documents; Detail names what was just finished.
type ProgressEvent struct {
	Stage  Stage
	Done   int
	Total  int
	Detail string
}

// Progress receives events. A nil Progress is valid and costs nothing.
type Progress func(ProgressEvent)

func (p Progress) emit(e ProgressEvent) {
	if p != nil {
		p(e)
	}
}
