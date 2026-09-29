package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/analyzer"
	"github.com/wyw14/solo-039/internal/campaign"
	"github.com/wyw14/solo-039/internal/column"
	"github.com/wyw14/solo-039/internal/feed"
	"github.com/wyw14/solo-039/internal/interlock"
	"github.com/wyw14/solo-039/internal/journal"
	"github.com/wyw14/solo-039/internal/model"
	"github.com/wyw14/solo-039/internal/product"
	"github.com/wyw14/solo-039/internal/reboiler"
	"github.com/wyw14/solo-039/internal/reflux"
	"github.com/wyw14/solo-039/internal/relief"
	"github.com/wyw14/solo-039/internal/utility"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	analyzerComponent             = "top"
	defaultCalibrationValiditySec = 7 * 24 * 3600
)

type Plant struct {
	mu                sync.RWMutex
	Campaign          *campaign.State
	Column            *column.State
	Feed              *feed.Coordinator
	Reflux            *reflux.Coordinator
	RefluxState       *reflux.State
	Reboiler          *reboiler.State
	Analyzer          *analyzer.State
	Product           *product.Coordinator
	Interlock         *interlock.Coordinator
	Relief            *relief.Coordinator
	Capacity          *utility.Capacity
	Leases            *utility.LeaseBook
	Journal           *journal.Store
	FileLog           *journal.FileLog
	Snapshots         *journal.SnapshotFile
	FeedMetrics       *feed.Metrics
	RefluxTelemetry   *reflux.Telemetry
	ReboilerTelemetry *reboiler.Telemetry
	ProductAudit      *product.Audit
	UtilityLedger     *utility.Ledger
	Metrics           *Metrics
	CampaignHistory   *campaign.History
	Profile           *column.Profile
	RefluxAllocator   *reflux.Allocator
	ReboilerCommands  *reboiler.CommandBook
	Evidence          *analyzer.EvidenceCatalog
	AnalyzerFile      *analyzer.StateFile
	ReliefHistory     *relief.History
	Incidents         *Incidents
	started           bool
	lastError         string
}

func NewPlant(dataDir string) *Plant {
	campaignState := campaign.New("baseline")
	analyzerState := analyzer.New()
	store := journal.New()
	refluxCoordinator := reflux.NewCoordinator(1)
	plant := &Plant{
		Campaign:          campaignState,
		Column:            column.New("T-101"),
		Feed:              feed.NewCoordinator(),
		Reflux:            refluxCoordinator,
		RefluxState:       reflux.New(),
		Reboiler:          reboiler.New(),
		Analyzer:          analyzerState,
		Product:           product.NewCoordinator(campaignState, analyzerState, store),
		Interlock:         interlock.NewCoordinator(),
		Relief:            relief.NewCoordinator(),
		Capacity:          utility.NewCapacity(1),
		Leases:            utility.NewLeaseBook(),
		Journal:           store,
		FileLog:           journal.NewFileLog(filepath.Join(dataDir, "events.jsonl")),
		Snapshots:         journal.NewSnapshotFile(filepath.Join(dataDir, "snapshot.json")),
		FeedMetrics:       feed.NewMetrics(),
		RefluxTelemetry:   reflux.NewTelemetry(),
		ReboilerTelemetry: reboiler.NewTelemetry(),
		ProductAudit:      product.NewAudit(),
		UtilityLedger:     utility.NewLedger(),
		Metrics:           NewMetrics(),
		CampaignHistory:   campaign.NewHistory(),
		Profile:           column.NewProfile(),
		RefluxAllocator:   reflux.NewAllocator(refluxCoordinator),
		ReboilerCommands:  reboiler.NewCommandBook(),
		Evidence:          analyzer.NewEvidenceCatalog(),
		AnalyzerFile:      analyzer.NewStateFile(filepath.Join(dataDir, "analyzer.json")),
		ReliefHistory:     relief.NewHistory(),
		Incidents:         NewIncidents(),
	}
	plant.Reflux.Register("T-101", plant.RefluxState)
	plant.Feed.Observe = plant.observeFeed
	plant.restoreAnalyzer()
	return plant
}

// restoreAnalyzer 从磁盘恢复校准证档案与历史原始响应；文件缺失（首次启动）不视为错误。
func (p *Plant) restoreAnalyzer() {
	recovery, err := p.AnalyzerFile.Load()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			p.lastError = err.Error()
		}
		return
	}
	analyzer.Restore(p.Analyzer, recovery)
}

func (p *Plant) saveAnalyzer() error {
	return p.AnalyzerFile.Save(p.Analyzer)
}

func (p *Plant) Start(ctx context.Context, grade, source string) (model.Campaign, error) {
	var operationErr error
	defer func() { p.Metrics.Record("start", operationErr) }()
	if err := model.ValidateGrade(grade); err != nil {
		operationErr = err
		return model.Campaign{}, err
	}
	p.mu.Lock()
	if p.started {
		current := p.Campaign.Snapshot()
		p.mu.Unlock()
		return current, errors.New("plant already started")
	}
	p.started = true
	p.lastError = ""
	p.mu.Unlock()
	current := p.Campaign.Snapshot()
	before := current
	current.Grade = grade
	campaign.Restore(p.Campaign, current)
	p.CampaignHistory.Record(before, current, "start grade selected")
	if err := p.Feed.Switch(ctx, source, current); err != nil {
		operationErr = err
		p.fail(err)
		return model.Campaign{}, err
	}
	p.Column.SetFeedRevision(p.Feed.Current.Revision)
	p.Analyzer.BindFeed(p.Feed.Current.Revision)
	p.Campaign.Advance(model.Warming)
	if err := p.Reboiler.Ramp(ctx, p.Campaign.Snapshot(), 0.5); err != nil {
		operationErr = err
		p.fail(err)
		return model.Campaign{}, err
	}
	p.Campaign.Advance(model.Stabilizing)
	p.Column.Apply(p.Campaign.Snapshot(), 1.1, 0.5)
	_ = p.Profile.Put(column.TrayReading{Tray: 1, Temperature: 118, Pressure: 1.1})
	_ = p.Profile.Put(column.TrayReading{Tray: 20, Temperature: 82, Pressure: 0.9})
	p.ReboilerTelemetry.Record(p.Reboiler.Snapshot())
	if err := p.record("plant.started", source); err != nil {
		p.fail(err)
		operationErr = err
		return model.Campaign{}, err
	}
	return p.Campaign.Snapshot(), nil
}

func (p *Plant) Transition(ctx context.Context, grade, source string) (model.Campaign, error) {
	if err := model.ValidateGrade(grade); err != nil {
		return model.Campaign{}, err
	}
	before := p.Campaign.Snapshot()
	current := p.Campaign.Advance(model.Transitioning)
	current.Grade = grade
	current.Revision = uuid.New()
	campaign.Restore(p.Campaign, current)
	p.CampaignHistory.Record(before, current, "grade transition")
	if err := p.Feed.Switch(ctx, source, current); err != nil {
		p.fail(err)
		return model.Campaign{}, err
	}
	p.Column.SetFeedRevision(p.Feed.Current.Revision)
	p.FeedMetrics.Record(source)
	p.Analyzer.BindFeed(p.Feed.Current.Revision)
	if err := p.record("campaign.transition", grade); err != nil {
		p.fail(err)
		return model.Campaign{}, err
	}
	return p.Campaign.Snapshot(), nil
}

// AddSample 接收一条分析仪原始响应。测量必须存在"测量时刻适用"的标准样校准证，
// 且响应落在证书标定覆盖的范围内；换算浓度随样本保存，原始响应留存。
func (p *Plant) AddSample(value float64, at int64) (int, error) {
	revision := p.Feed.Current.Revision
	sample := model.Sample{ID: uuid.New(), FeedRevision: revision, At: at, Component: analyzerComponent, Value: value, Valid: true}
	cert, hasCert := p.Analyzer.CertificateAt(analyzerComponent, at)
	if !hasCert {
		return p.Analyzer.SampleCount(), errors.New("no effective calibration certificate for this measurement")
	}
	if !cert.CoversResponse(value) {
		return p.Analyzer.SampleCount(), fmt.Errorf("analyzer response %.4g is outside calibrated range %.4g..%.4g", value, cert.MinResponse, cert.MaxResponse)
	}
	if !p.Analyzer.Add(sample) {
		return p.Analyzer.SampleCount(), errors.New("sample rejected by current evidence generation")
	}
	if err := p.saveAnalyzer(); err != nil {
		p.fail(err)
		return p.Analyzer.SampleCount(), err
	}
	return p.Analyzer.SampleCount(), nil
}

// IssueCalibration 用标准样参考浓度与仪器响应拟合并发放校准证，
// 记录适用组分、浓度范围、残差与有效期，并写入日志。
func (p *Plant) IssueCalibration(input analyzer.CertificateInput) (analyzer.Certificate, error) {
	if input.Component == "" {
		input.Component = analyzerComponent
	}
	now := time.Now().Unix()
	if input.IssuedAt == 0 {
		input.IssuedAt = now
	}
	if input.ValidFrom == 0 {
		input.ValidFrom = input.IssuedAt
	}
	if input.ValidUntil == 0 {
		input.ValidUntil = input.ValidFrom + defaultCalibrationValiditySec
	}
	cert, err := p.Analyzer.IssueCalibration(input)
	if err != nil {
		return analyzer.Certificate{}, err
	}
	p.Analyzer.BindFeed(p.Feed.Current.Revision)
	if err := p.saveAnalyzer(); err != nil {
		p.fail(err)
		return analyzer.Certificate{}, err
	}
	payload := fmt.Sprintf("%s|%s|%.6g..%.6g|%d..%d",
		cert.ID, cert.Component, cert.MinConcentration, cert.MaxConcentration, cert.ValidFrom, cert.ValidUntil)
	if err := p.record("analyzer.calibrated", payload); err != nil {
		p.fail(err)
		return analyzer.Certificate{}, err
	}
	return cert, nil
}

// RevokeCalibration 撤销校准证：证书档案保留，相关组分证明标记为需重新评估，
// 事件写入日志，之后的产品放行必须基于仍有效的证书。
func (p *Plant) RevokeCalibration(id uuid.UUID, reason string) (analyzer.Certificate, error) {
	cert, err := p.Analyzer.RevokeCalibration(id, time.Now().Unix(), reason)
	if err != nil {
		return analyzer.Certificate{}, err
	}
	marked := p.Evidence.MarkStale(id.String())
	if err := p.saveAnalyzer(); err != nil {
		p.fail(err)
		return analyzer.Certificate{}, err
	}
	payload := fmt.Sprintf("%s|%s|stale_proofs=%d", cert.ID, reason, marked)
	if err := p.record("analyzer.calibration_revoked", payload); err != nil {
		p.fail(err)
		return analyzer.Certificate{}, err
	}
	return cert, nil
}

// CalibrationCertificates 返回校准证档案（含已撤销、已过期的）。
func (p *Plant) CalibrationCertificates() []analyzer.Certificate {
	return p.Analyzer.Registry().List()
}

func (p *Plant) RouteProduct(target string) error {
	current := p.Campaign.Snapshot()
	proof, ok := p.Analyzer.Proof(current.Revision)
	if !ok {
		return errors.New("composition evidence is incomplete")
	}
	// 生产放行必须使用测量时刻适用的校准证：证书已撤销、已过期或证明已被标记
	// 需要重新评估时，拒绝放行并要求重新标定/重新取证。
	certID, parseErr := uuid.Parse(proof.Source)
	if parseErr != nil {
		return errors.New("composition evidence is not bound to a calibration certificate")
	}
	cert, certOK := p.Analyzer.Registry().Get(certID)
	if !certOK || !cert.EffectiveAt(proof.At) {
		return errors.New("calibration certificate is no longer effective; composition evidence must be reassessed")
	}
	if proof.Stale {
		return errors.New("composition evidence requires reassessment after certificate revocation")
	}
	_ = p.Evidence.Add(proof)
	pressureProof := model.Proof{ID: uuid.New(), Revision: current.Revision, Source: "column-pressure", Digest: fmt.Sprintf("%.3f", p.Column.Snapshot().Pressure), Durable: true, Independent: true, At: time.Now().Unix()}
	permit := p.Interlock.Evaluate([]model.Proof{proof, pressureProof}, model.Campaign{ID: current.ID, Revision: current.Revision, Phase: model.OnSpec, Epoch: current.Epoch, Fencing: current.Fencing})
	if err := permit.Error(); err != nil {
		return err
	}
	current.Phase = model.OnSpec
	campaign.Restore(p.Campaign, current)
	plan := model.ProductPlan{Campaign: current.ID, Revision: current.Revision, Grade: current.Grade, Target: target}
	if err := p.Product.PublishWithProof(plan, proof); err != nil {
		p.ProductAudit.Record(plan, proof, false, err.Error())
		p.fail(err)
		return err
	}
	p.ProductAudit.Record(plan, proof, true, "durable proof accepted")
	return p.record("product.routed", target)
}

func (p *Plant) AcquireReflux(owner string, amount float64) (model.Lease, error) {
	current := p.Campaign.Snapshot()
	lease, err := p.Reflux.Acquire(owner, amount, current.Fencing)
	if err != nil {
		p.Incidents.Open("reflux-capacity", err.Error(), current.Revision.String())
		return model.Lease{}, err
	}
	p.Leases.Put(lease)
	p.UtilityLedger.Commit(lease)
	p.RefluxTelemetry.Record(p.RefluxState.Snapshot())
	if err := p.record("reflux.lease", owner); err != nil {
		p.Reflux.Release(lease)
		return model.Lease{}, err
	}
	return lease, nil
}

func (p *Plant) Shutdown(ctx context.Context, reliefTimeout bool) (relief.ShutdownResult, error) {
	current := p.Campaign.Advance(model.Shutting)
	reliefErr := p.Relief.Shutdown(ctx, current, "flare", reliefTimeout)
	steamErr := p.ReboilerCommands.Close(ctx, p.Reboiler, current)
	p.ReboilerTelemetry.Record(p.Reboiler.Snapshot())
	result := relief.CombineShutdown(reliefErr, steamErr, p.Relief.State.Snapshot(), p.Reboiler.ClosureConfirmed(current.Epoch))
	p.ReliefHistory.Record(p.Relief.State.Snapshot(), reliefErr)
	if err := result.Error(); err != nil {
		p.Incidents.Open("shutdown", err.Error(), current.Revision.String())
		p.fail(err)
		return result, err
	}
	p.Campaign.Stop()
	p.mu.Lock()
	p.started = false
	p.mu.Unlock()
	return result, p.record("plant.stopped", "safe")
}

func (p *Plant) Snapshot() model.Snapshot {
	return model.Snapshot{Campaign: p.Campaign.Snapshot(), Column: p.Column.Snapshot(), Reflux: p.RefluxState.Snapshot(), Reboiler: p.Reboiler.Snapshot(), Products: p.ProductTargets(), Relief: p.Relief.State.Snapshot()}
}

func (p *Plant) Save() error {
	if err := p.Snapshots.Save(p.Snapshot()); err != nil {
		return err
	}
	return p.saveAnalyzer()
}

func (p *Plant) Restore() error {
	snapshot, err := p.Snapshots.Load()
	if err != nil {
		return err
	}
	campaign.Restore(p.Campaign, snapshot.Campaign)
	column.Restore(p.Column, snapshot.Column)
	if err := reboiler.Restore(p.Reboiler, snapshot.Reboiler, snapshot.Campaign); err != nil {
		return err
	}
	if err := journal.ValidateRecovery(snapshot, p.Journal.Events()); err != nil {
		return err
	}
	p.mu.Lock()
	p.started = snapshot.Campaign.Phase != model.Stopped
	p.mu.Unlock()
	if !model.PhaseAllowsHeat(snapshot.Campaign.Phase) {
		p.Reboiler.Stop()
	}
	return nil
}

func (p *Plant) ProductTargets() []string {
	if p.Product.Route.Opened() {
		return []string{p.Product.Route.Target()}
	}
	return []string{}
}

func (p *Plant) Status() map[string]any {
	p.mu.RLock()
	started := p.started
	lastError := p.lastError
	p.mu.RUnlock()
	gradient, _ := p.Profile.Gradient()
	certificates := p.Analyzer.Registry().List()
	now := time.Now().Unix()
	var activeCertificate any
	activeValid := false
	if cert, ok := p.Analyzer.CurrentCertificate(); ok {
		activeCertificate = cert
		activeValid = cert.EffectiveAt(now)
	}
	return map[string]any{"started": started, "last_error": lastError, "campaign": p.Campaign.Snapshot(), "column": p.Column.Snapshot(), "reflux_used": p.Reflux.Used(), "products": p.ProductTargets(), "relief": p.Relief.State.Snapshot(), "feed_metrics": p.FeedMetrics.Snapshot(), "reflux_telemetry": p.RefluxTelemetry.Snapshot(), "reboiler_telemetry": p.ReboilerTelemetry.Snapshot(), "product_decisions": p.ProductAudit.Count(), "utility_used": p.UtilityLedger.Used(), "metrics": p.Metrics.Snapshot(), "campaign_changes": p.CampaignHistory.Count(), "profile_gradient": gradient, "profile_stable": p.Profile.Stable(40), "evidence_count": p.Evidence.Count(p.Campaign.Snapshot().Revision.String()), "calibrations": certificates, "active_calibration": activeCertificate, "active_calibration_effective": activeValid, "raw_responses": len(p.Analyzer.RawResponses()), "relief_attempts": p.ReliefHistory.Count(), "open_incidents": p.Incidents.CountOpen(), "operational_audit": p.OperationalAudit()}
}

func (p *Plant) record(kind, payload string) error {
	event := model.NewEvent(kind, p.Campaign.Snapshot(), payload)
	event.At = time.Now().UnixNano()
	if err := p.Journal.Append(event); err != nil {
		return err
	}
	if err := p.FileLog.Append(event); err != nil {
		return err
	}
	return p.Save()
}

func (p *Plant) observeFeed(value feed.Transition) {
	payload := fmt.Sprintf("%s:%s:%t:%.2f:%s:%t", value.Stage, value.OldSource, value.OldOpen, value.OldFlow, value.NewSource, value.NewOpen)
	if err := p.record("feed.transition", payload); err != nil {
		p.fail(err)
	}
	if value.Stage == "new-active" {
		p.FeedMetrics.Record(value.NewSource)
	}
}

func (p *Plant) fail(err error) {
	p.mu.Lock()
	p.lastError = err.Error()
	p.mu.Unlock()
	p.Incidents.Open("runtime", err.Error(), p.Campaign.Snapshot().Revision.String())
}
