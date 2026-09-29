package analyzer

import (
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

type RecoveryState struct {
	Current      uuid.UUID
	Certificates []model.CalibrationCertificate
	FeedRevision uuid.UUID
	Window       []model.Sample
	Samples      []model.Sample
	Needed       int
	MaxGap       int64
}

// Capture 固化分析仪证据链：全部校准证（含已撤销）、当前窗口与全部原始响应归档。
func Capture(state *State) RecoveryState {
	_, feedRevision := state.window.Identity()
	needed, maxGap := state.window.Config()
	return RecoveryState{
		Current:      state.book.CurrentID(),
		Certificates: state.book.List(),
		FeedRevision: feedRevision,
		Window:       state.window.Samples(),
		Samples:      state.ArchivedSamples(),
		Needed:       needed,
		MaxGap:       maxGap,
	}
}

// Restore 重建证书簿、窗口与原始响应归档。证书撤销状态随证一起恢复，
// 撤销前出具的证明可据此再次判定为需重新评估。
func Restore(state *State, recovery RecoveryState) {
	state.book.Restore(recovery.Certificates)
	state.mu.Lock()
	state.feedRevision = recovery.FeedRevision
	state.archive = append(state.archive[:0], recovery.Samples...)
	state.mu.Unlock()
	windowSamples := recovery.Window
	if windowSamples == nil {
		// 从不可裁剪的原始响应归档中，按当前代次重建证据窗口。
		for _, sample := range recovery.Samples {
			if sample.Valid && sample.Calibration == recovery.Current && sample.FeedRevision == recovery.FeedRevision {
				windowSamples = append(windowSamples, sample)
			}
		}
	}
	state.window.Configure(recovery.Needed, recovery.MaxGap)
	state.window.Reset(recovery.Current, recovery.FeedRevision)
	for _, sample := range windowSamples {
		state.window.Add(sample)
	}
}

func Compatible(recovery RecoveryState, calibration, feedRevision uuid.UUID) bool {
	return recovery.Current == calibration && recovery.FeedRevision == feedRevision
}
