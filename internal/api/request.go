package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

type transitionRequest struct {
	Grade  string `json:"grade"`
	Source string `json:"source"`
}
type sampleRequest struct {
	Component string  `json:"component"`
	Response  float64 `json:"response"`
	At        int64   `json:"at"`
}
type calibrationPointRequest struct {
	Reference float64 `json:"reference"`
	Response  float64 `json:"response"`
}
type issueCertificateRequest struct {
	Component string                    `json:"component"`
	Points    []calibrationPointRequest `json:"points"`
	IssuedAt  int64                     `json:"issued_at"`
	ExpiresAt int64                     `json:"expires_at"`
}
type revokeCertificateRequest struct {
	Reason string `json:"reason"`
	At     int64  `json:"at"`
}
type routeRequest struct {
	Target string `json:"target"`
}
type leaseRequest struct {
	Owner  string  `json:"owner"`
	Amount float64 `json:"amount"`
}

func decodeJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	return nil
}

func boolQuery(r *http.Request, key string) (bool, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, errors.New("invalid boolean query")
	}
	return value, nil
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}
