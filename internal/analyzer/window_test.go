package analyzer

import (
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
	"testing"
)

func TestWindowRejectsGenerationMismatch(t *testing.T) {
	window := NewWindow()
	calibration := uuid.New()
	feedRevision := uuid.New()
	window.Reset(calibration, feedRevision)
	if window.Add(model.Sample{Certificate: uuid.New(), FeedRevision: feedRevision, Valid: true, At: 1}) {
		t.Fatal("accepted another calibration")
	}
}
