package analyzer

import (
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

// CertificateSourcePrefix 是分析仪证明 Source 字段中校准证 ID 的统一前缀，
// 撤销证书时据此把相关证明判定为失效。
const CertificateSourcePrefix = "calibration-certificate:"

// CertificateSource 把证书 ID 转成证明来源标识。
func CertificateSource(id uuid.UUID) string { return CertificateSourcePrefix + id.String() }

// CertificateIDFromSource 从证明来源中解析证书 ID；非证书来源时返回 false。
func CertificateIDFromSource(source string) (uuid.UUID, bool) {
	if !strings.HasPrefix(source, CertificateSourcePrefix) {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimPrefix(source, CertificateSourcePrefix))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// CertificateBook 登记簿保存全部标准样校准证。签发与撤销都只追加/打标记，
// 证书及其标准样原始数据永不删除，保证每次标定的适用范围与依据可追溯。
type CertificateBook struct {
	mu         sync.RWMutex
	current    uuid.UUID
	lastIssued uuid.UUID
	certs      map[uuid.UUID]model.CalibrationCertificate
	order      []uuid.UUID
}

func NewCertificateBook() *CertificateBook {
	return &CertificateBook{certs: make(map[uuid.UUID]model.CalibrationCertificate)}
}

// Issue 登记一张新签发的校准证，并把它置为当前代次。
func (b *CertificateBook) Issue(cert model.CalibrationCertificate) error {
	if cert.ID == uuid.Nil {
		return errors.New("certificate id is missing")
	}
	if cert.Digest == "" || model.CertificateDigest(cert) != cert.Digest {
		return errors.New("certificate digest does not match its content")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.certs[cert.ID]; exists {
		return errors.New("certificate already registered")
	}
	b.certs[cert.ID] = cert
	b.order = append(b.order, cert.ID)
	b.current = cert.ID
	b.lastIssued = cert.ID
	return nil
}

// Revoke 软撤销证书。被撤销的证仍可查，但不再适用，依据它出具的证明需重新评估。
func (b *CertificateBook) Revoke(id uuid.UUID, at int64, reason string) (model.CalibrationCertificate, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cert, ok := b.certs[id]
	if !ok {
		return model.CalibrationCertificate{}, errors.New("certificate not found")
	}
	if cert.RevokedAt != 0 {
		return cert, errors.New("certificate already revoked")
	}
	cert.RevokedAt = at
	cert.RevokeReason = reason
	b.certs[id] = cert
	if b.current == id {
		b.current = uuid.Nil
	}
	return cert, nil
}

// Current 返回当前代次证书；当前证被撤销或从未签发时第二个返回值为 false。
// 是否在有效期内请用 ApplicableAt 判断。
func (b *CertificateBook) Current() (model.CalibrationCertificate, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.current == uuid.Nil {
		return model.CalibrationCertificate{}, false
	}
	cert, ok := b.certs[b.current]
	return cert, ok
}

// Get 按 ID 取证书（含已撤销/已过期证书）。
func (b *CertificateBook) Get(id uuid.UUID) (model.CalibrationCertificate, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	cert, ok := b.certs[id]
	return cert, ok
}

// ApplicableAt 返回在 at 时刻适用于指定组分、且证书有效（未撤销、在有效期内）
// 的当前代次证书。生产样本必须使用测量时适用的证书，过期或撤销后不再出证。
func (b *CertificateBook) ApplicableAt(component string, at int64) (model.CalibrationCertificate, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.current == uuid.Nil {
		return model.CalibrationCertificate{}, errors.New("no active calibration certificate")
	}
	cert, ok := b.certs[b.current]
	if !ok {
		return model.CalibrationCertificate{}, errors.New("active calibration certificate is missing")
	}
	if cert.RevokedAt != 0 {
		return model.CalibrationCertificate{}, errors.New("calibration certificate has been revoked")
	}
	if at < cert.IssuedAt || at > cert.ExpiresAt {
		return model.CalibrationCertificate{}, errors.New("calibration certificate is not valid at sample time")
	}
	if cert.Component != component {
		return model.CalibrationCertificate{}, errors.New("calibration certificate is for another component")
	}
	return cert, nil
}

// List 按签发顺序返回全部证书（含已撤销），供审计与查询。
func (b *CertificateBook) List() []model.CalibrationCertificate {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]model.CalibrationCertificate, 0, len(b.order))
	for _, id := range b.order {
		out = append(out, b.certs[id])
	}
	return out
}

// CurrentID 返回当前代次证书 ID（可能为 uuid.Nil）。
func (b *CertificateBook) CurrentID() uuid.UUID {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.current
}

// Last 返回最近签发的证书（即便它已撤销或过期），用于把无证书适用时的
// 原始读数仍然挂到最近代次上归档；从未签发时返回 false。
func (b *CertificateBook) Last() (model.CalibrationCertificate, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.lastIssued == uuid.Nil {
		return model.CalibrationCertificate{}, false
	}
	cert, ok := b.certs[b.lastIssued]
	return cert, ok
}

// Restore 用持久化数据重建登记簿，并以最后一张有效证书作为当前代次。
func (b *CertificateBook) Restore(certs []model.CalibrationCertificate) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.certs = make(map[uuid.UUID]model.CalibrationCertificate, len(certs))
	b.order = b.order[:0]
	b.current = uuid.Nil
	b.lastIssued = uuid.Nil
	for _, cert := range certs {
		if _, ok := b.certs[cert.ID]; ok {
			continue
		}
		b.certs[cert.ID] = cert
		b.order = append(b.order, cert.ID)
		b.lastIssued = cert.ID
		if cert.RevokedAt == 0 {
			b.current = cert.ID
		}
	}
}
