package model

import (
	"math"
	"testing"
)

func TestFitCalibrationLinear(t *testing.T) {
	points := []CalibrationPoint{
		{Reference: 10, Response: 1},
		{Reference: 20, Response: 2},
		{Reference: 30, Response: 3},
	}
	fit, err := FitCalibration(points)
	if err != nil {
		t.Fatalf("fit failed: %v", err)
	}
	if math.Abs(fit.Slope-10) > 1e-9 || math.Abs(fit.Intercept) > 1e-9 {
		t.Fatalf("unexpected relation: slope=%v intercept=%v", fit.Slope, fit.Intercept)
	}
	if len(fit.Residuals) != 3 {
		t.Fatalf("residuals not recorded per point: %v", fit.Residuals)
	}
	for i, residual := range fit.Residuals {
		if math.Abs(residual) > 1e-9 {
			t.Fatalf("point %d residual %v is not zero on a perfect line", i, residual)
		}
	}
	if math.Abs(fit.RSquared-1) > 1e-9 {
		t.Fatalf("perfect line should have R^2=1, got %v", fit.RSquared)
	}
}

func TestFitCalibrationRecordsResiduals(t *testing.T) {
	points := []CalibrationPoint{
		{Reference: 10, Response: 1},
		{Reference: 20, Response: 2},
		{Reference: 31, Response: 3},
	}
	fit, err := FitCalibration(points)
	if err != nil {
		t.Fatalf("fit failed: %v", err)
	}
	// y = 10.5x - 0.666... ；残差分别为 1/6、-1/3、1/6
	if math.Abs(fit.Slope-10.5) > 1e-9 {
		t.Fatalf("unexpected slope %v", fit.Slope)
	}
	if math.Abs(fit.MaxAbsResidual-1.0/3.0) > 1e-9 {
		t.Fatalf("unexpected max residual %v", fit.MaxAbsResidual)
	}
	if fit.RSquared >= 1 {
		t.Fatalf("imperfect fit R^2 must be below 1, got %v", fit.RSquared)
	}
}

func TestFitCalibrationRejectsDegeneratePoints(t *testing.T) {
	if _, err := FitCalibration([]CalibrationPoint{{Reference: 1, Response: 1}}); err == nil {
		t.Fatal("single point must not fit")
	}
	if _, err := FitCalibration([]CalibrationPoint{{Reference: 1, Response: 2}, {Reference: 2, Response: 2}}); err == nil {
		t.Fatal("identical responses must not fit")
	}
}

func TestIssueCertificateScopeAndValidity(t *testing.T) {
	points := []CalibrationPoint{{Reference: 10, Response: 1}, {Reference: 50, Response: 5}}
	cert, err := IssueCertificate("top", points, 100, 200)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if cert.Component != "top" || cert.MinReference != 10 || cert.MaxReference != 50 {
		t.Fatalf("scope not recorded: %+v", cert)
	}
	if !cert.Active(150) || cert.Active(99) || cert.Active(201) {
		t.Fatal("validity window enforced incorrectly")
	}
	if !cert.Covers("top", 30) || cert.Covers("side", 30) || cert.Covers("top", 60) {
		t.Fatal("scope coverage enforced incorrectly")
	}
	if got := cert.Concentration(2); math.Abs(got-20) > 1e-9 {
		t.Fatalf("relation conversion wrong: %v", got)
	}
	if CertificateDigest(cert) != cert.Digest {
		t.Fatal("digest does not verify")
	}
	tampered := cert
	tampered.Component = "side"
	if CertificateDigest(tampered) == cert.Digest {
		t.Fatal("digest must change when scope is tampered")
	}
}

func TestIssueCertificateValidation(t *testing.T) {
	points := []CalibrationPoint{{Reference: 10, Response: 1}, {Reference: 50, Response: 5}}
	if _, err := IssueCertificate("", points, 100, 200); err == nil {
		t.Fatal("missing component must fail")
	}
	if _, err := IssueCertificate("top", points, 200, 100); err == nil {
		t.Fatal("expiry before issue must fail")
	}
	if _, err := IssueCertificate("top", []CalibrationPoint{{Reference: 10, Response: 1}, {Reference: 10, Response: 2}}, 100, 200); err == nil {
		t.Fatal("zero-width concentration range must fail")
	}
}
