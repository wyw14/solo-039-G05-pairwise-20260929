package analyzer

import (
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
	"sync"
)

type Window struct {
	mu           sync.Mutex
	samples      []model.Sample
	calibration  uuid.UUID
	feedRevision uuid.UUID
	needed       int
	maxGap       int64
}

func NewWindow() *Window {
	return &Window{samples: make([]model.Sample, 0, 16), calibration: uuid.New(), needed: 3, maxGap: 60}
}

func (w *Window) Reset(calibration, feedRevision uuid.UUID) {
	w.mu.Lock()
	w.samples = nil
	w.calibration = calibration
	w.feedRevision = feedRevision
	w.mu.Unlock()
}

func (w *Window) Add(sample model.Sample) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !sample.Valid || sample.Calibration != w.calibration || sample.FeedRevision != w.feedRevision {
		return false
	}
	if len(w.samples) > 0 {
		last := w.samples[len(w.samples)-1]
		if sample.At <= last.At {
			return false
		}
		if sample.At-last.At > w.maxGap {
			w.samples = w.samples[:0]
		}
	}
	w.samples = append(w.samples, sample)
	return true
}

func (w *Window) Configure(needed int, maxGap int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if needed > 0 {
		w.needed = needed
	}
	if maxGap > 0 {
		w.maxGap = maxGap
	}
}

func (w *Window) Identity() (uuid.UUID, uuid.UUID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calibration, w.feedRevision
}

// Config 返回窗口所需样本数与最大采样间隔，供恢复使用。
func (w *Window) Config() (int, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.needed, w.maxGap
}

func (w *Window) Complete() bool { w.mu.Lock(); defer w.mu.Unlock(); return len(w.samples) >= w.needed }

func (w *Window) Samples() []model.Sample {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]model.Sample(nil), w.samples...)
}
