package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
)

// CalibrationPoint 是一个标准样测量点：标准样的已知参考浓度与仪器对它的原始响应。
type CalibrationPoint struct {
	Reference float64 `json:"reference"`
	Response  float64 `json:"response"`
}

// CalibrationFit 是由标准样拟合得到的校准关系：浓度 = Slope*响应 + Intercept。
// Residuals 逐点记录 参考浓度 - 拟合浓度，原始数据是否可追溯一目了然。
type CalibrationFit struct {
	Slope          float64   `json:"slope"`
	Intercept      float64   `json:"intercept"`
	Residuals      []float64 `json:"residuals"`
	MaxAbsResidual float64   `json:"max_abs_residual"`
	RSquared       float64   `json:"r_squared"`
}

// CalibrationCertificate 标准样校准证：说明本次标定对哪个组分、
// 哪个浓度范围有效，拟合关系、残差与有效期一并固化在证上。
// 撤销是软标记（RevokedAt/RevokeReason），证与原始标准样数据永不删除。
type CalibrationCertificate struct {
	ID           uuid.UUID          `json:"id"`
	Component    string             `json:"component"`
	MinReference float64            `json:"min_reference"`
	MaxReference float64            `json:"max_reference"`
	Points       []CalibrationPoint `json:"points"`
	Fit          CalibrationFit     `json:"fit"`
	IssuedAt     int64              `json:"issued_at"`
	ExpiresAt    int64              `json:"expires_at"`
	RevokedAt    int64              `json:"revoked_at"`
	RevokeReason string             `json:"revoke_reason"`
	Digest       string             `json:"digest"`
}

// Active 表示证书在 at 时刻既未撤销也仍在有效期内。
func (c CalibrationCertificate) Active(at int64) bool {
	return c.RevokedAt == 0 && at >= c.IssuedAt && at <= c.ExpiresAt
}

// Covers 判断证书是否适用于指定组分、且给出的浓度落在标定范围内。
func (c CalibrationCertificate) Covers(component string, concentration float64) bool {
	return c.Component == component &&
		concentration >= c.MinReference && concentration <= c.MaxReference
}

// Concentration 用证上的校准关系把仪器原始响应换算成浓度。
func (c CalibrationCertificate) Concentration(response float64) float64 {
	return c.Fit.Slope*response + c.Fit.Intercept
}

// FitCalibration 对标准样做最小二乘线性拟合：以响应为 x、参考浓度为 y，
// 拟合 y = slope*x + intercept，并计算逐点残差与决定系数 R²。
func FitCalibration(points []CalibrationPoint) (CalibrationFit, error) {
	if len(points) < 2 {
		return CalibrationFit{}, errors.New("calibration needs at least two standard samples")
	}
	n := float64(len(points))
	var sumX, sumY, sumXY, sumXX float64
	for _, point := range points {
		if math.IsNaN(point.Reference) || math.IsInf(point.Reference, 0) ||
			math.IsNaN(point.Response) || math.IsInf(point.Response, 0) {
			return CalibrationFit{}, errors.New("calibration point contains invalid number")
		}
		sumX += point.Response
		sumY += point.Reference
		sumXY += point.Response * point.Reference
		sumXX += point.Response * point.Response
	}
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return CalibrationFit{}, errors.New("standard responses are identical, cannot fit a relation")
	}
	fit := CalibrationFit{Slope: (n*sumXY - sumX*sumY) / denominator}
	fit.Intercept = (sumY - fit.Slope*sumX) / n

	meanY := sumY / n
	var sse, sst float64
	fit.Residuals = make([]float64, len(points))
	for i, point := range points {
		predicted := fit.Slope*point.Response + fit.Intercept
		residual := point.Reference - predicted
		fit.Residuals[i] = residual
		if abs := math.Abs(residual); abs > fit.MaxAbsResidual {
			fit.MaxAbsResidual = abs
		}
		sse += residual * residual
		sst += (point.Reference - meanY) * (point.Reference - meanY)
	}
	switch {
	case sst == 0 && sse == 0:
		fit.RSquared = 1
	case sst == 0:
		fit.RSquared = 0
	default:
		fit.RSquared = 1 - sse/sst
	}
	return fit, nil
}

// IssueCertificate 用一组标准样签发校准证：拟合关系、记录残差、
// 限定组分与浓度范围并设定有效期。
func IssueCertificate(component string, points []CalibrationPoint, issuedAt, expiresAt int64) (CalibrationCertificate, error) {
	component = strings.TrimSpace(component)
	if component == "" {
		return CalibrationCertificate{}, errors.New("component is required")
	}
	if len(component) > 32 {
		return CalibrationCertificate{}, errors.New("component is too long")
	}
	if issuedAt <= 0 {
		return CalibrationCertificate{}, errors.New("issued_at is required")
	}
	if expiresAt <= issuedAt {
		return CalibrationCertificate{}, errors.New("expires_at must be after issued_at")
	}
	if len(points) < 2 {
		return CalibrationCertificate{}, errors.New("calibration needs at least two standard samples")
	}
	fit, err := FitCalibration(points)
	if err != nil {
		return CalibrationCertificate{}, err
	}
	cert := CalibrationCertificate{
		ID:        uuid.New(),
		Component: component,
		Points:    append([]CalibrationPoint(nil), points...),
		Fit:       fit,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}
	cert.MinReference = points[0].Reference
	cert.MaxReference = points[0].Reference
	for _, point := range points {
		if point.Reference < cert.MinReference {
			cert.MinReference = point.Reference
		}
		if point.Reference > cert.MaxReference {
			cert.MaxReference = point.Reference
		}
	}
	if cert.MinReference == cert.MaxReference {
		return CalibrationCertificate{}, errors.New("standard references must span a concentration range")
	}
	cert.Digest = CertificateDigest(cert)
	return cert, nil
}

// CertificateDigest 对证上关键内容取摘要，任何对组分、范围、标准样、
// 拟合结果或有效期的篡改都会使摘要失配。
func CertificateDigest(c CalibrationCertificate) string {
	value := fmt.Sprintf("%s|%s|%f|%f|%d|%d|%f|%f|%f",
		c.ID, c.Component, c.MinReference, c.MaxReference, c.IssuedAt, c.ExpiresAt,
		c.Fit.Slope, c.Fit.Intercept, c.Fit.MaxAbsResidual)
	for _, point := range c.Points {
		value += fmt.Sprintf("|%f:%f", point.Reference, point.Response)
	}
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

// AnalyzerSnapshot 持久化分析仪证据链：全部校准证（含已撤销）、
// 当前代次与全部原始样本响应。
type AnalyzerSnapshot struct {
	Current      uuid.UUID                `json:"current"`
	Certificates []CalibrationCertificate `json:"certificates"`
	Samples      []Sample                 `json:"samples"`
}
