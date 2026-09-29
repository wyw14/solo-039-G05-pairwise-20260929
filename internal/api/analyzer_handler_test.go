package api

import (
	"bytes"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/wyw14/solo-039/internal/service"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func jsonI64(v int64) string { return strconv.FormatInt(v, 10) }

func newTestServer(t *testing.T) *Server {
	t.Helper()
	plant := service.NewPlant(t.TempDir())
	server := &Server{plant: plant, router: chi.NewRouter()}
	server.routes()
	return server
}

func do(t *testing.T, handler http.Handler, method, path string, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode %s: %v (body=%s)", path, err, rec.Body.String())
		}
	}
	return rec.Code, decoded
}

func TestCalibrationCertificateLifecycleHTTP(t *testing.T) {
	server := newTestServer(t)
	h := server.Handler()

	// 启动装置
	if code, _ := do(t, h, "POST", "/api/campaign/start?grade=baseline&source=primary", ""); code != http.StatusAccepted {
		t.Fatalf("start status = %d", code)
	}

	// 未校准先采样 -> 409
	if code, body := do(t, h, "POST", "/api/analyzer/samples", `{"value":3.0}`); code != http.StatusConflict {
		t.Fatalf("sample before calibration: code=%d body=%v", code, body)
	}

	// 发放标准样校准证
	payload := `{"component":"top","points":[{"reference":0.5,"response":2.0},{"reference":1.0,"response":3.0},{"reference":1.5,"response":4.0},{"reference":2.0,"response":5.0}]}`
	code, cert := do(t, h, "POST", "/api/analyzer/calibrate", payload)
	if code != http.StatusAccepted {
		t.Fatalf("calibrate status = %d body=%v", code, cert)
	}
	if cert["component"] != "top" || cert["min_concentration"] != 0.5 || cert["max_concentration"] != 2.0 {
		t.Fatalf("certificate scope wrong: %v", cert)
	}
	if cert["valid_until"].(float64) <= cert["valid_from"].(float64) {
		t.Fatal("certificate must carry a validity period")
	}
	residuals, _ := cert["residuals"].([]any)
	if len(residuals) != 4 {
		t.Fatalf("residuals recorded = %d", len(residuals))
	}
	certID := cert["id"].(string)
	validFrom := int64(cert["valid_from"].(float64))

	// 档案可查
	code, list := do(t, h, "GET", "/api/analyzer/calibrations", "")
	if code != http.StatusOK || len(list["certificates"].([]any)) != 1 {
		t.Fatalf("list calibrations: code=%d body=%v", code, list)
	}

	// 采 3 个样并放行（时刻严格递增、在有效期内）
	for i := 0; i < 3; i++ {
		body := `{"value":3.0,"at":` + jsonI64(validFrom+int64(i*10)) + `}`
		if code, body := do(t, h, "POST", "/api/analyzer/samples", body); code != http.StatusAccepted {
			t.Fatalf("sample %d: code=%d body=%v", i, code, body)
		}
	}
	if code, body := do(t, h, "POST", "/api/products/route", `{"target":"tank-a"}`); code != http.StatusAccepted {
		t.Fatalf("route product: code=%d body=%v", code, body)
	}

	// 撤销证书
	revokeBody := `{"id":"` + certID + `","reason":"standard gas expired"}`
	if code, body := do(t, h, "POST", "/api/analyzer/calibrations/revoke", revokeBody); code != http.StatusAccepted {
		t.Fatalf("revoke: code=%d body=%v", code, body)
	}

	// 撤销后无有效证明，再放行被拒
	if code, _ := do(t, h, "POST", "/api/products/route", `{"target":"tank-a"}`); code != http.StatusConflict {
		t.Fatalf("route after revocation should be 409, got %d", code)
	}

	// 撤销错误 ID
	if code, _ := do(t, h, "POST", "/api/analyzer/calibrations/revoke", `{"id":"not-a-uuid"}`); code != http.StatusBadRequest {
		t.Fatalf("bad uuid should be 400, got %d", code)
	}

	// 发证输入不足（少于两个标准样）
	if code, _ := do(t, h, "POST", "/api/analyzer/calibrate", `{"component":"top","points":[{"reference":1,"response":2}]}`); code != http.StatusConflict {
		t.Fatalf("insufficient standards should be 409, got %d", code)
	}

	// 运行状态包含校准档案且审计映射可构建
	if code, body := do(t, h, "GET", "/api/operations", ""); code != http.StatusOK {
		t.Fatalf("operations: %d", code)
	} else {
		if body["raw_responses"] == nil {
			t.Fatal("operations status should expose raw response count")
		}
	}
}
