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
	Value float64 `json:"value"`
	At    int64   `json:"at"`
}
type calibrateRequest struct {
	Component  string            `json:"component"`
	Points     []calibPointInput `json:"points"`
	ValidFrom  int64             `json:"valid_from"`
	ValidUntil int64             `json:"valid_until"`
}
type calibPointInput struct {
	Reference float64 `json:"reference"`
	Response  float64 `json:"response"`
}
type revokeCalibrationRequest struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
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
