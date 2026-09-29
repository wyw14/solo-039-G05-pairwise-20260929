package analyzer

import (
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
	"sync"
)

type State struct {
	mu sync.RWMutex
	// registry 保存所有标准样校准证（含已撤销/过期的，永不删除）。
	registry *Registry
	// current 是当前生效校准证；为空表示尚未标定，不能采生产样。
	current uuid.UUID
	window  *Window
	// responses 留存每一条原始仪器响应，证书撤销或重新标定都不删除。
	responses []model.Sample
	last      model.Proof
	online    bool
}

func New() *State {
	w := NewWindow()
	w.Reset(uuid.Nil, uuid.Nil)
	return &State{registry: NewRegistry(), window: w, online: true}
}

func (s *State) SetOnline(value bool) { s.mu.Lock(); s.online = value; s.mu.Unlock() }

// Registry 暴露校准证登记表。
func (s *State) Registry() *Registry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.registry
}

// IssueCalibration 用标准样（参考浓度 + 仪器响应）拟合并发放新校准证，
// 返回的证书记录了适用组分、浓度范围、残差与有效期。换发新证不撤销旧证：
// 旧证在其有效期内仍对历史测量有效。
func (s *State) IssueCalibration(input CertificateInput) (Certificate, error) {
	cert, err := s.registry.Issue(input)
	if err != nil {
		return Certificate{}, err
	}
	s.mu.Lock()
	s.current = cert.ID
	_, feedRevision := s.window.Identity()
	s.window.Reset(cert.ID, feedRevision)
	s.mu.Unlock()
	return *cert, nil
}

// RevokeCalibration 撤销校准证。撤销后窗口立即失效，相关生产证明需要重新评估。
func (s *State) RevokeCalibration(id uuid.UUID, at int64, reason string) (Certificate, error) {
	cert, err := s.registry.Revoke(id, at, reason)
	if err != nil {
		return Certificate{}, err
	}
	s.mu.Lock()
	if s.current == id {
		s.current = uuid.Nil
		_, feedRevision := s.window.Identity()
		s.window.Reset(uuid.Nil, feedRevision)
	}
	s.mu.Unlock()
	return cert, nil
}

// CurrentCertificate 返回当前校准证副本。
func (s *State) CurrentCertificate() (Certificate, bool) {
	s.mu.RLock()
	id := s.current
	s.mu.RUnlock()
	if id == uuid.Nil {
		return Certificate{}, false
	}
	return s.registry.Get(id)
}

// Calibration 返回当前校准证 ID（无有效证书时为 uuid.Nil）。
func (s *State) Calibration() uuid.UUID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *State) BindFeed(revision uuid.UUID) {
	s.mu.Lock()
	s.window.Reset(s.current, revision)
	s.mu.Unlock()
}

// Add 接收一条生产测量。测量必须能找到"测量时刻适用"的校准证（未撤销、在有效期内、
// 组分一致），且仪器响应落在标定覆盖范围内；换算出的浓度随样本一并记录，
// 原始响应原样留存。
func (s *State) Add(sample model.Sample) bool {
	s.mu.RLock()
	online := s.online
	s.mu.RUnlock()
	if !online {
		return false
	}

	cert, ok := s.registry.Effective(sample.Component, sample.At)
	if !ok {
		return false
	}
	if !cert.CoversResponse(sample.Value) {
		return false
	}

	sample.Certificate = cert.ID
	sample.Valid = true
	sample.Concentration = cert.Concentration(sample.Value)

	if !s.window.Add(sample) {
		return false
	}
	s.mu.Lock()
	s.responses = append(s.responses, sample)
	s.mu.Unlock()
	return true
}

// Proof 生成组分证明。证明来源绑定到测量时适用的校准证，
// 证书撤销后可凭 Source 定位并要求重新评估。
func (s *State) Proof(revision uuid.UUID) (model.Proof, bool) {
	s.mu.RLock()
	online := s.online
	s.mu.RUnlock()
	if !online {
		return model.Proof{}, false
	}
	samples := s.window.Samples()
	if !s.window.Complete() {
		return model.Proof{}, false
	}
	certID, _ := s.window.Identity()
	at := s.window.LatestAt()
	component := samples[0].Component
	cert, ok := s.registry.Get(certID)
	if !ok || !cert.EffectiveAt(at) || cert.Component != component {
		return model.Proof{}, false
	}
	proof := model.Proof{ID: uuid.New(), Revision: revision, Source: certID.String(), At: at, Independent: true}
	proof.Digest = model.ProofDigest(proof)
	s.mu.Lock()
	s.last = proof
	s.mu.Unlock()
	return proof, true
}

func (s *State) LastProof() model.Proof { s.mu.RLock(); defer s.mu.RUnlock(); return s.last }

func (s *State) ConfigureWindow(needed int, maxGap int64) {
	s.mu.RLock()
	window := s.window
	s.mu.RUnlock()
	window.Configure(needed, maxGap)
}

func (s *State) SampleCount() int {
	s.mu.RLock()
	window := s.window
	s.mu.RUnlock()
	return len(window.Samples())
}

// RawResponses 返回全部历史原始响应的副本（证书撤销/换发均不删除）。
func (s *State) RawResponses() []model.Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]model.Sample(nil), s.responses...)
}

// CertificateAt 返回某组分在测量时刻适用的证书。
func (s *State) CertificateAt(component string, at int64) (Certificate, bool) {
	return s.registry.Effective(component, at)
}
