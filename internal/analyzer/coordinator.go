package analyzer

import (
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

type Coordinator struct{ State *State }

func NewCoordinator() *Coordinator { return &Coordinator{State: New()} }

func (c *Coordinator) Begin(revision uuid.UUID) { c.State.BindFeed(revision) }

// Record 以恢复重放方式补录一条样本，仍要求其校准证在测量时刻适用。
func (c *Coordinator) Record(sample model.Sample) bool { return c.State.Add(sample) }

// Measure 记录生产样本的仪器原始响应，由测量时适用的校准证换算浓度。
func (c *Coordinator) Measure(component string, response float64, at int64) (model.Sample, error) {
	return c.State.Measure(component, response, at)
}

func (c *Coordinator) CurrentProof(revision uuid.UUID) (model.Proof, bool) {
	return c.State.Proof(revision)
}
