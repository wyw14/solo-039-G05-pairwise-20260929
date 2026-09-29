package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

// listCertificates 返回全部标准样校准证（含已撤销）、当前代次及证书撤销后
// 待重新评估的产品放行决策。
func (s *Server) listCertificates(w http.ResponseWriter, r *http.Request) {
	current, certificates := s.plant.CalibrationCertificates()
	writeJSON(w, http.StatusOK, map[string]any{
		"current":              current,
		"certificates":         certificates,
		"raw_responses":        s.plant.RawResponseCount(),
		"pending_reevaluation": s.plant.PendingReevaluations(),
	})
}

// issueCertificate 输入组分、标准样参考浓度与仪器响应、有效期，
// 拟合校准关系并签发校准证。
func (s *Server) issueCertificate(w http.ResponseWriter, r *http.Request) {
	var input issueCertificateRequest
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now().Unix()
	if input.IssuedAt == 0 {
		input.IssuedAt = now
	}
	if input.ExpiresAt == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_at is required"})
		return
	}
	points := make([]model.CalibrationPoint, 0, len(input.Points))
	for _, point := range input.Points {
		points = append(points, model.CalibrationPoint{Reference: point.Reference, Response: point.Response})
	}
	cert, err := s.plant.IssueCalibrationCertificate(input.Component, points, input.IssuedAt, input.ExpiresAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, cert)
}

// revokeCertificate 撤销校准证；响应中列出受影响、需重新评估的产品代次。
func (s *Server) revokeCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid certificate id"})
		return
	}
	var input revokeCertificateRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if input.At == 0 {
		input.At = time.Now().Unix()
	}
	if input.Reason == "" {
		input.Reason = "manually revoked"
	}
	cert, affected, err := s.plant.RevokeCalibrationCertificate(id, input.Reason, input.At)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"certificate": cert, "affected_revisions": affected})
}
