package analyzer

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

type State struct {
	mu           sync.RWMutex
	book         *CertificateBook
	window       *Window
	archive      []model.Sample
	feedRevision uuid.UUID
	last         model.Proof
	online       bool
}

func New() *State {
	window := NewWindow()
	window.Reset(uuid.Nil, uuid.Nil)
	return &State{book: NewCertificateBook(), window: window, archive: make([]model.Sample, 0, 64), online: true}
}

func (s *State) SetOnline(value bool) {
	s.mu.Lock()
	s.online = value
	s.mu.Unlock()
}

// CurrentCertificate 返回当前代次校准证。
func (s *State) CurrentCertificate() (model.CalibrationCertificate, bool) {
	return s.book.Current()
}

// Book 暴露证书登记簿，供查询、撤销与持久化使用。
func (s *State) Book() *CertificateBook { return s.book }

// IssueCertificate 用标准样签发新校准证并切换当前代次。
// 旧代次的原始样本响应保留在归档中，不会因换代而丢失。
func (s *State) IssueCertificate(component string, points []model.CalibrationPoint, issuedAt, expiresAt int64) (model.CalibrationCertificate, error) {
	cert, err := model.IssueCertificate(component, points, issuedAt, expiresAt)
	if err != nil {
		return model.CalibrationCertificate{}, err
	}
	if err := s.book.Issue(cert); err != nil {
		return model.CalibrationCertificate{}, err
	}
	s.mu.Lock()
	feedRevision := s.feedRevision
	s.mu.Unlock()
	s.window.Reset(cert.ID, feedRevision)
	return cert, nil
}

// BindFeed 在进料代次变化时重新绑定证据窗口；窗口中的原始响应此前已归档，不会丢失。
func (s *State) BindFeed(revision uuid.UUID) {
	s.mu.Lock()
	s.feedRevision = revision
	calibration := s.book.CurrentID()
	s.mu.Unlock()
	s.window.Reset(calibration, revision)
}

// Measure 记录一次生产样本：必须使用测量时刻适用的校准证（组分相符、在有效期内、
// 未撤销，且换算浓度落在证上浓度范围内），用证上拟合关系把原始响应换算为浓度。
// 无论是否被证据窗口接受——哪怕证书缺失、过期、撤销或读数越界——原始响应都进入
// 归档（不适用的读数标记为无效但同样保留），原始响应永不丢失。
func (s *State) Measure(component string, response float64, at int64) (model.Sample, error) {
	s.mu.RLock()
	online := s.online
	feedRevision := s.feedRevision
	s.mu.RUnlock()
	if !online {
		return model.Sample{}, errors.New("analyzer is offline")
	}
	cert, certErr := s.book.ApplicableAt(component, at)
	if certErr != nil {
		// 证书不适用：读数仍挂到最近签发的证书代次上（没有则 Nil）并归档为无效。
		nearest, ok := s.book.Last()
		calibration := uuid.Nil
		if ok {
			calibration = nearest.ID
		}
		sample := model.Sample{ID: uuid.New(), FeedRevision: feedRevision, Calibration: calibration, At: at, Component: component, Response: response, Valid: false}
		s.archiveSample(sample)
		return sample, certErr
	}
	concentration := cert.Concentration(response)
	if !cert.Covers(component, concentration) {
		sample := model.Sample{ID: uuid.New(), FeedRevision: feedRevision, Calibration: cert.ID, At: at, Component: component, Response: response, Value: concentration, Valid: false}
		s.archiveSample(sample)
		return sample, errors.New("response converts outside the certified concentration range")
	}
	sample := model.Sample{ID: uuid.New(), FeedRevision: feedRevision, Calibration: cert.ID, At: at, Component: component, Response: response, Value: concentration, Valid: true}
	if !s.window.Add(sample) {
		s.archiveSample(sample)
		return sample, errors.New("sample rejected by current evidence generation")
	}
	s.archiveSample(sample)
	return sample, nil
}

// Add 接受已成形的样本（恢复路径使用），仍强制证书在测量时刻适用。
func (s *State) Add(sample model.Sample) bool {
	s.mu.RLock()
	online := s.online
	s.mu.RUnlock()
	if !online {
		return false
	}
	cert, ok := s.book.Get(sample.Calibration)
	if !ok || !cert.Active(sample.At) || cert.Component != sample.Component {
		sample.Valid = false
		s.archiveSample(sample)
		return false
	}
	if !cert.Covers(sample.Component, sample.Value) {
		sample.Valid = false
		s.archiveSample(sample)
		return false
	}
	sample.Valid = true
	accepted := s.window.Add(sample)
	s.archiveSample(sample)
	return accepted
}

func (s *State) archiveSample(sample model.Sample) {
	s.mu.Lock()
	s.archive = append(s.archive, sample)
	s.mu.Unlock()
}

// Proof 出具当前证据窗口的组分证明。只有在出证时刻证书仍然有效
// （未撤销、未过有效期）才能出证；Source 记录证书 ID，撤销时据此定位需重新评估的证明。
func (s *State) Proof(revision uuid.UUID) (model.Proof, bool) {
	return s.ProofAt(revision, time.Now().Unix())
}

// ProofAt 以显式时刻出证，便于测试与恢复重放。
func (s *State) ProofAt(revision uuid.UUID, at int64) (model.Proof, bool) {
	s.mu.RLock()
	online := s.online
	s.mu.RUnlock()
	complete := s.window.Complete()
	calibration, _ := s.window.Identity()
	if !online || !complete || calibration == uuid.Nil {
		return model.Proof{}, false
	}
	cert, ok := s.book.Get(calibration)
	if !ok || cert.RevokedAt != 0 || at < cert.IssuedAt || at > cert.ExpiresAt {
		return model.Proof{}, false
	}
	proof := model.Proof{ID: uuid.New(), Revision: revision, Source: CertificateSource(cert.ID), Independent: true, At: at}
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

// ArchivedSamples 返回全部原始响应归档（含换代前、越界与无效响应），永不裁剪。
func (s *State) ArchivedSamples() []model.Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]model.Sample(nil), s.archive...)
}
