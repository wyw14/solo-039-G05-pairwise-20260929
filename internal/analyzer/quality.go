package analyzer

import (
	"errors"
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

type Quality struct {
	Minimum      int
	MaxDeviation float64
}

func DefaultQuality() Quality { return Quality{Minimum: 3, MaxDeviation: 0.08} }

func (q Quality) Evaluate(samples []model.Sample) error {
	if len(samples) < q.Minimum {
		return errors.New("not enough analyzer samples")
	}
	average := 0.0
	for _, sample := range samples {
		average += sample.Concentration
	}
	average /= float64(len(samples))
	for _, sample := range samples {
		if sample.Concentration-average > q.MaxDeviation || average-sample.Concentration > q.MaxDeviation {
			return errors.New("analyzer samples are unstable")
		}
	}
	return nil
}

func (q Quality) Proof(state *State, revision string) (model.Proof, error) {
	samples := state.window.Samples()
	if err := q.Evaluate(samples); err != nil {
		return model.Proof{}, err
	}
	parsed, err := uuid.Parse(revision)
	if err != nil {
		return model.Proof{}, err
	}
	proof, ok := state.Proof(parsed)
	if !ok {
		return model.Proof{}, errors.New("analyzer proof unavailable")
	}
	return proof, nil
}
