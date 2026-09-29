package analyzer

import (
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

type Coordinator struct{ State *State }

func NewCoordinator() *Coordinator { return &Coordinator{State: New()} }

func (c *Coordinator) Begin(revision uuid.UUID) { c.State.BindFeed(revision) }

// Record 记录一条仪器原始响应。是否有测量时刻适用的校准证、是否落在标定范围内，
// 由 State.Add 统一判定；不满足时返回 false。
func (c *Coordinator) Record(revision uuid.UUID, component string, value float64, at int64) bool {
	return c.State.Add(model.Sample{ID: uuid.New(), FeedRevision: revision, Component: component, Value: value, At: at})
}

// IssueCalibration 发放标准样校准证。
func (c *Coordinator) IssueCalibration(input CertificateInput) (Certificate, error) {
	return c.State.IssueCalibration(input)
}

// RevokeCalibration 撤销校准证。
func (c *Coordinator) RevokeCalibration(id uuid.UUID, at int64, reason string) (Certificate, error) {
	return c.State.RevokeCalibration(id, at, reason)
}

func (c *Coordinator) CurrentProof(revision uuid.UUID) (model.Proof, bool) {
	return c.State.Proof(revision)
}
