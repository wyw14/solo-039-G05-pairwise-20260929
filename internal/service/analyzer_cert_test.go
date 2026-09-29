package service

import (
	"context"
	"github.com/wyw14/solo-039/internal/analyzer"
	"testing"
	"time"
)

func newStartedPlant(t *testing.T) *Plant {
	t.Helper()
	plant := NewPlant(t.TempDir())
	if _, err := plant.Start(context.Background(), "baseline", "primary"); err != nil {
		t.Fatalf("start plant: %v", err)
	}
	return plant
}

func standardPoints() []analyzer.CalibrationPoint {
	return []analyzer.CalibrationPoint{
		{Reference: 0.5, Response: 2.0},
		{Reference: 1.0, Response: 3.0},
		{Reference: 1.5, Response: 4.0},
		{Reference: 2.0, Response: 5.0},
	}
}

func TestSamplingRequiresEffectiveCertificate(t *testing.T) {
	plant := newStartedPlant(t)
	if _, err := plant.AddSample(3.0, time.Now().Unix()); err == nil {
		t.Fatal("production sample without a calibration certificate must be rejected")
	}
	cert, err := plant.IssueCalibration(analyzer.CertificateInput{Component: "top", Points: standardPoints()})
	if err != nil {
		t.Fatalf("issue calibration: %v", err)
	}
	if cert.MinConcentration != 0.5 || cert.MaxConcentration != 2.0 {
		t.Fatalf("calibrated concentration range wrong: %v..%v", cert.MinConcentration, cert.MaxConcentration)
	}
	if cert.MaxAbsResidual != 0 || cert.RMSE != 0 {
		t.Fatalf("linear standards should fit exactly: rmse=%v", cert.RMSE)
	}

	// 响应 3 -> 浓度 1，落在 [2,5] 响应覆盖区间内
	at := cert.ValidFrom
	for i := 0; i < 3; i++ {
		if _, err := plant.AddSample(3.0+float64(i%2)*0.01, at+int64(i*10)); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	// 超出标定响应范围
	if _, err := plant.AddSample(9.0, at+40); err == nil {
		t.Fatal("response outside calibrated response range must be rejected")
	}
}

func TestRouteUsesCertificateThenRevocationRequiresReassessment(t *testing.T) {
	plant := newStartedPlant(t)
	cert, err := plant.IssueCalibration(analyzer.CertificateInput{Component: "top", Points: standardPoints()})
	if err != nil {
		t.Fatalf("issue calibration: %v", err)
	}
	at := cert.ValidFrom
	for i := 0; i < 3; i++ {
		if _, err := plant.AddSample(3.0, at+int64(i*10)); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	if err := plant.RouteProduct("tank-a"); err != nil {
		t.Fatalf("route with effective certificate: %v", err)
	}
	if len(plant.Analyzer.RawResponses()) != 3 {
		t.Fatal("routed samples should be retained")
	}

	// 撤销证书：已有证明标记失效，放行必须重新取证
	revoked, err := plant.RevokeCalibration(cert.ID, "standard gas out of spec")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !revoked.Revoked {
		t.Fatal("certificate should be flagged revoked")
	}
	if stale := len(plant.Evidence.Stale(plant.Campaign.Snapshot().Revision.String())); stale != 1 {
		t.Fatalf("stale proofs = %d, want 1", stale)
	}
	if err := plant.RouteProduct("tank-a"); err == nil {
		t.Fatal("routing after revocation without reassessment must fail")
	}
	// 原始响应未删除
	if len(plant.Analyzer.RawResponses()) != 3 {
		t.Fatal("raw responses must not be deleted when certificate is revoked")
	}

	// 重新发证并重新采样后才能再次放行
	newCert, err := plant.IssueCalibration(analyzer.CertificateInput{Component: "top", Points: standardPoints()})
	if err != nil {
		t.Fatalf("re-issue calibration: %v", err)
	}
	at = newCert.ValidFrom
	for i := 0; i < 3; i++ {
		if _, err := plant.AddSample(3.0, at+int64(i*10)); err != nil {
			t.Fatalf("new sample %d: %v", i, err)
		}
	}
	if err := plant.RouteProduct("tank-b"); err != nil {
		t.Fatalf("route after reassessment: %v", err)
	}
	if len(plant.Analyzer.RawResponses()) != 6 {
		t.Fatal("all raw responses across both certificate generations must be retained")
	}
	// 旧证书记录仍可查
	old, ok := plant.Analyzer.Registry().Get(cert.ID)
	if !ok || !old.Revoked {
		t.Fatal("revoked certificate must remain in the calibration record book")
	}
}

func TestExpiredCertificateRejectsMeasurement(t *testing.T) {
	plant := newStartedPlant(t)
	now := time.Now().Unix()
	cert, err := plant.IssueCalibration(analyzer.CertificateInput{
		Component: "top", Points: standardPoints(),
		ValidFrom: now - 200, ValidUntil: now - 100,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, has := plant.Analyzer.CurrentCertificate(); !has {
		t.Fatal("expired certificate is still the current issued one")
	}
	if _, err := plant.AddSample(3.0, now); err == nil {
		t.Fatal("measurement outside certificate validity must be rejected")
	}
	if cert.EffectiveAt(now) {
		t.Fatal("expired certificate must not be effective")
	}
}

func TestCalibrationStatePersistedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	plant := NewPlant(dir)
	if _, err := plant.Start(context.Background(), "baseline", "primary"); err != nil {
		t.Fatalf("start: %v", err)
	}
	cert, err := plant.IssueCalibration(analyzer.CertificateInput{Component: "top", Points: standardPoints()})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := plant.AddSample(3.0, cert.ValidFrom); err != nil {
		t.Fatalf("sample: %v", err)
	}

	reloaded := NewPlant(dir)
	got, ok := reloaded.Analyzer.Registry().Get(cert.ID)
	if !ok {
		t.Fatal("certificate must survive process restart")
	}
	if got.MaxConcentration != 2.0 || len(got.Points) != 4 {
		t.Fatal("certificate content must survive restart intact")
	}
	if len(reloaded.Analyzer.RawResponses()) != 1 {
		t.Fatal("raw responses must survive process restart")
	}
}
