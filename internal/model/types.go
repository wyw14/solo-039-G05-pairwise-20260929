package model

import "github.com/google/uuid"

type Phase string

const (
	Preparing     Phase = "preparing"
	Warming       Phase = "warming"
	Stabilizing   Phase = "stabilizing"
	OnSpec        Phase = "on-spec"
	Transitioning Phase = "transitioning"
	Shutting      Phase = "shutting"
	Stopped       Phase = "stopped"
)

type Campaign struct {
	ID       uuid.UUID
	Grade    string
	Revision uuid.UUID
	Phase    Phase
	Epoch    uint64
	Pressure float64
	Level    float64
	Fencing  uint64
}

type Sample struct {
	ID           uuid.UUID `json:"id"`
	FeedRevision uuid.UUID `json:"feed_revision"`
	Calibration  uuid.UUID `json:"calibration"`
	At           int64     `json:"at"`
	Component    string    `json:"component"`
	// Response 是仪器原始响应，Value 是用测量时适用的校准证换算出的浓度。
	// 原始响应随证据链长期保留，不随证书代次切换而丢弃。
	Response float64 `json:"response"`
	Value    float64 `json:"value"`
	Valid    bool    `json:"valid"`
}

type Proof struct {
	ID          uuid.UUID `json:"id"`
	Revision    uuid.UUID `json:"revision"`
	Source      string    `json:"source"`
	Digest      string    `json:"digest"`
	Durable     bool      `json:"durable"`
	Independent bool      `json:"independent"`
	// Invalidated 表示证明所依据的校准证已被撤销，证明必须重新评估；
	// 记录本身不删除，保留完整审计轨迹。
	Invalidated bool  `json:"invalidated"`
	At          int64 `json:"at"`
}

type ProductPlan struct {
	Campaign uuid.UUID
	Revision uuid.UUID
	Grade    string
	Target   string
}

type Lease struct {
	ID       uuid.UUID
	Owner    string
	Capacity float64
	Fence    uint64
	Active   bool
}

type Event struct {
	ID       uuid.UUID
	Kind     string
	Campaign uuid.UUID
	Revision uuid.UUID
	Epoch    uint64
	Payload  string
	At       int64
}

type Snapshot struct {
	Campaign Campaign
	Column   Column
	Reflux   Reflux
	Reboiler Reboiler
	Products []string
	Relief   Relief
	Analyzer AnalyzerSnapshot
}

type Column struct {
	ID           string
	Pressure     float64
	Temperature  float64
	Level        float64
	FeedRevision uuid.UUID
	Phase        Phase
	Fencing      uint64
}

type Reflux struct {
	Ratio        float64
	PumpCapacity float64
	Lease        Lease
	CommandEpoch uint64
}

type Reboiler struct {
	Steam        float64
	Closed       bool
	CommandEpoch uint64
	Trip         uint64
}

type Relief struct {
	Valve    string
	Pressure float64
	TimedOut bool
	Released bool
}
