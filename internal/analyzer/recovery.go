package analyzer

import (
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

// RecoveryState 是分析仪的完整可恢复状态：校准证档案（含已撤销的）、
// 当前适用证书、窗口与全部历史原始响应。
type RecoveryState struct {
	Certificates []Certificate
	Current      uuid.UUID
	FeedRevision uuid.UUID
	Samples      []model.Sample
	Responses    []model.Sample
}

func Capture(state *State) RecoveryState {
	_, feedRevision := state.window.Identity()
	return RecoveryState{
		Certificates: state.registry.Snapshot(),
		Current:      state.Calibration(),
		FeedRevision: feedRevision,
		Samples:      state.window.Samples(),
		Responses:    state.RawResponses(),
	}
}

// Restore 重建分析仪状态。校准证（含已撤销/过期）与历史原始响应全部保留，
// 窗口按证书恢复：当前证书已撤销或窗口证书不再适用时窗口清空。
func Restore(state *State, recovery RecoveryState) {
	state.mu.Lock()
	state.registry.Restore(recovery.Certificates)
	state.current = uuid.Nil
	if cert, ok := state.registry.Get(recovery.Current); ok && !cert.Revoked {
		state.current = recovery.Current
	}
	state.window.Reset(state.current, recovery.FeedRevision)
	state.responses = append([]model.Sample(nil), recovery.Responses...)
	samples := append([]model.Sample(nil), recovery.Samples...)
	active := state.current
	state.mu.Unlock()
	// 窗口样本已包含在恢复的原始响应台账中，这里只重建窗口，不再重复登记台账。
	// 当前证书已撤销/换发时窗口证书不匹配，样本会被窗口拒绝（窗口本应作废）。
	if active != uuid.Nil {
		for _, sample := range samples {
			state.window.Add(sample)
		}
	}
}

// Compatible 判断恢复出的窗口是否仍与指定证书/进料修订匹配，
// 且该证书此刻仍适用。
func Compatible(recovery RecoveryState, certificate, feedRevision uuid.UUID) bool {
	if recovery.Current != certificate || recovery.FeedRevision != feedRevision {
		return false
	}
	for _, cert := range recovery.Certificates {
		if cert.ID == certificate {
			return !cert.Revoked
		}
	}
	return false
}
