package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"math"
	"sort"
	"sync"
)

// CalibrationPoint 是一个标准样标定点：参考浓度与该浓度下的仪器原始响应。
type CalibrationPoint struct {
	Reference float64 `json:"reference"`
	Response  float64 `json:"response"`
}

// Residual 记录单个标定点的拟合残差（浓度单位）。
type Residual struct {
	Reference float64 `json:"reference"`
	Response  float64 `json:"response"`
	Predicted float64 `json:"predicted"`
	Value     float64 `json:"value"` // 参考浓度 - 拟合反算浓度
}

// Certificate 是标准样校准证：说明一次标定对哪个组分、什么浓度范围、什么时间段有效，
// 并保存拟合关系、原始标定点与残差。撤销与到期只改变适用性，记录本身永不删除。
type Certificate struct {
	ID        uuid.UUID `json:"id"`
	Component string    `json:"component"`

	// 适用浓度范围（由标准样参考浓度确定）与对应的仪器响应覆盖范围。
	MinConcentration float64 `json:"min_concentration"`
	MaxConcentration float64 `json:"max_concentration"`
	MinResponse      float64 `json:"min_response"`
	MaxResponse      float64 `json:"max_response"`

	// 拟合关系：response = Slope*concentration + Intercept。
	Slope     float64 `json:"slope"`
	Intercept float64 `json:"intercept"`

	// 标准样原始数据与残差，发证后不可变。
	Points         []CalibrationPoint `json:"points"`
	Residuals      []Residual         `json:"residuals"`
	RMSE           float64            `json:"rmse"`
	MaxAbsResidual float64            `json:"max_abs_residual"`

	ValidFrom  int64 `json:"valid_from"`
	ValidUntil int64 `json:"valid_until"`
	IssuedAt   int64 `json:"issued_at"`

	Revoked      bool   `json:"revoked"`
	RevokedAt    int64  `json:"revoked_at"`
	RevokeReason string `json:"revoke_reason"`

	Digest string `json:"digest"`
}

// CertificateInput 是发证请求。
type CertificateInput struct {
	Component  string
	Points     []CalibrationPoint
	ValidFrom  int64
	ValidUntil int64
	IssuedAt   int64
}

// EffectiveAt 判断证书在给定测量时刻是否适用：已发证、未撤销、在有效期内。
func (c *Certificate) EffectiveAt(at int64) bool {
	if c.Revoked {
		return false
	}
	if at < c.ValidFrom || at > c.ValidUntil {
		return false
	}
	return true
}

// CoversConcentration 判断浓度是否落在标定范围内。
func (c *Certificate) CoversConcentration(concentration float64) bool {
	return concentration >= c.MinConcentration && concentration <= c.MaxConcentration
}

// CoversResponse 判断仪器原始响应是否落在标定点覆盖的响应范围内。
func (c *Certificate) CoversResponse(response float64) bool {
	return response >= c.MinResponse && response <= c.MaxResponse
}

// Concentration 用拟合关系把仪器原始响应换算成浓度。
func (c *Certificate) Concentration(response float64) float64 {
	return (response - c.Intercept) / c.Slope
}

// Fit 对标准样做最小二乘线性拟合（response = slope*concentration + intercept），
// 并逐点计算残差。
func Fit(points []CalibrationPoint) (slope, intercept, rmse, maxAbs float64, residuals []Residual, err error) {
	if len(points) < 2 {
		return 0, 0, 0, 0, nil, errors.New("calibration requires at least two standard samples")
	}
	n := float64(len(points))
	var sumX, sumY, sumXY, sumXX float64
	for _, point := range points {
		sumX += point.Reference
		sumY += point.Response
		sumXY += point.Reference * point.Response
		sumXX += point.Reference * point.Reference
	}
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return 0, 0, 0, 0, nil, errors.New("standard samples need distinct reference concentrations")
	}
	slope = (n*sumXY - sumX*sumY) / denominator
	if math.IsNaN(slope) || math.IsInf(slope, 0) || slope == 0 {
		return 0, 0, 0, 0, nil, errors.New("calibration slope is degenerate")
	}
	intercept = (sumY - slope*sumX) / n

	residuals = make([]Residual, len(points))
	var sqSum float64
	for i, point := range points {
		predicted := (point.Response - intercept) / slope
		value := point.Reference - predicted
		residuals[i] = Residual{Reference: point.Reference, Response: point.Response, Predicted: predicted, Value: value}
		sqSum += value * value
		abs := value
		if abs < 0 {
			abs = -abs
		}
		if abs > maxAbs {
			maxAbs = abs
		}
	}
	rmse = math.Sqrt(sqSum / n)
	return slope, intercept, rmse, maxAbs, residuals, nil
}

func certificateDigest(c Certificate) string {
	raw := fmt.Sprintf("%s|%s|%.12g:%.12g|%d:%d|%d|%t:%d",
		c.ID, c.Component, c.Slope, c.Intercept,
		c.ValidFrom, c.ValidUntil, c.IssuedAt, c.Revoked, c.RevokedAt)
	for _, point := range c.Points {
		raw += fmt.Sprintf("|%.12g:%.12g", point.Reference, point.Response)
	}
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

// Registry 保存全部校准证，支持按组分/时间检索适用证书。
type Registry struct {
	mu      sync.RWMutex
	ordered []*Certificate
	byID    map[uuid.UUID]*Certificate
}

func NewRegistry() *Registry {
	return &Registry{byID: make(map[uuid.UUID]*Certificate)}
}

// Issue 用标准样数据拟合并发放一张新校准证。原始标定点原样保留。
func (r *Registry) Issue(input CertificateInput) (*Certificate, error) {
	if input.Component == "" {
		return nil, errors.New("component is required")
	}
	if len(input.Points) < 2 {
		return nil, errors.New("calibration requires at least two standard samples")
	}
	if input.ValidFrom <= 0 || input.ValidUntil <= input.ValidFrom {
		return nil, errors.New("validity period is invalid")
	}
	points := append([]CalibrationPoint(nil), input.Points...)
	for _, point := range points {
		if point.Reference < 0 {
			return nil, errors.New("reference concentration cannot be negative")
		}
		if math.IsNaN(point.Reference) || math.IsNaN(point.Response) ||
			math.IsInf(point.Reference, 0) || math.IsInf(point.Response, 0) {
			return nil, errors.New("standard sample values must be finite")
		}
	}
	slope, intercept, rmse, maxAbs, residuals, err := Fit(points)
	if err != nil {
		return nil, err
	}
	cert := &Certificate{
		ID:               uuid.New(),
		Component:        input.Component,
		Slope:            slope,
		Intercept:        intercept,
		Points:           points,
		Residuals:        residuals,
		RMSE:             rmse,
		MaxAbsResidual:   maxAbs,
		ValidFrom:        input.ValidFrom,
		ValidUntil:       input.ValidUntil,
		IssuedAt:         input.IssuedAt,
		MinConcentration: points[0].Reference,
		MaxConcentration: points[0].Reference,
		MinResponse:      points[0].Response,
		MaxResponse:      points[0].Response,
	}
	for _, point := range points[1:] {
		cert.MinConcentration = math.Min(cert.MinConcentration, point.Reference)
		cert.MaxConcentration = math.Max(cert.MaxConcentration, point.Reference)
		cert.MinResponse = math.Min(cert.MinResponse, point.Response)
		cert.MaxResponse = math.Max(cert.MaxResponse, point.Response)
	}
	cert.Digest = certificateDigest(*cert)

	r.mu.Lock()
	r.byID[cert.ID] = cert
	r.ordered = append(r.ordered, cert)
	r.mu.Unlock()
	return cert, nil
}

// Get 返回证书副本，找不到时第二返回值为 false。
func (r *Registry) Get(id uuid.UUID) (Certificate, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cert, ok := r.byID[id]
	if !ok {
		return Certificate{}, false
	}
	return cert.clone(), true
}

// Revoke 撤销证书。撤销后历史记录仍保留，但所有测量时刻不再适用。
func (r *Registry) Revoke(id uuid.UUID, at int64, reason string) (Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cert, ok := r.byID[id]
	if !ok {
		return Certificate{}, errors.New("calibration certificate not found")
	}
	if cert.Revoked {
		return cert.clone(), errors.New("calibration certificate already revoked")
	}
	cert.Revoked = true
	cert.RevokedAt = at
	cert.RevokeReason = reason
	cert.Digest = certificateDigest(*cert)
	return cert.clone(), nil
}

// Effective 返回某组分在测量时刻适用（未撤销、在有效期内）的最新证书。
func (r *Registry) Effective(component string, at int64) (Certificate, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := len(r.ordered) - 1; i >= 0; i-- {
		cert := r.ordered[i]
		if cert.Component == component && cert.EffectiveAt(at) {
			return cert.clone(), true
		}
	}
	return Certificate{}, false
}

// List 返回按发证时间排序的全部证书副本（含已撤销、已过期的）。
func (r *Registry) List() []Certificate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Certificate, 0, len(r.ordered))
	for _, cert := range r.ordered {
		out = append(out, cert.clone())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].IssuedAt < out[j].IssuedAt })
	return out
}

// Snapshot 返回有序证书副本，用于恢复。
func (r *Registry) Snapshot() []Certificate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Certificate, 0, len(r.ordered))
	for _, cert := range r.ordered {
		out = append(out, cert.clone())
	}
	return out
}

// Restore 用证书快照重建注册表。
func (r *Registry) Restore(certs []Certificate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID = make(map[uuid.UUID]*Certificate, len(certs))
	r.ordered = make([]*Certificate, 0, len(certs))
	for i := range certs {
		cert := certs[i].clone()
		r.byID[cert.ID] = &cert
		r.ordered = append(r.ordered, &cert)
	}
	sort.SliceStable(r.ordered, func(i, j int) bool { return r.ordered[i].IssuedAt < r.ordered[j].IssuedAt })
}

func (c *Certificate) clone() Certificate {
	clone := *c
	clone.Points = append([]CalibrationPoint(nil), c.Points...)
	clone.Residuals = append([]Residual(nil), c.Residuals...)
	return clone
}
