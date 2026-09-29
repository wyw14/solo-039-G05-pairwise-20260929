package analyzer

import (
	"math"
	"testing"
)

// 构造一条 response = 2*concentration + 1 的理想标定线。
func idealPoints() []CalibrationPoint {
	return []CalibrationPoint{
		{Reference: 0, Response: 1},
		{Reference: 1, Response: 3},
		{Reference: 2, Response: 5},
		{Reference: 3, Response: 7},
	}
}

func TestFitLinearRelationship(t *testing.T) {
	slope, intercept, rmse, maxAbs, residuals, err := Fit(idealPoints())
	if err != nil {
		t.Fatalf("fit failed: %v", err)
	}
	if math.Abs(slope-2) > 1e-12 {
		t.Fatalf("slope = %v, want 2", slope)
	}
	if math.Abs(intercept-1) > 1e-12 {
		t.Fatalf("intercept = %v, want 1", intercept)
	}
	if rmse != 0 || maxAbs != 0 {
		t.Fatalf("perfect fit should have zero residuals, got rmse=%v max=%v", rmse, maxAbs)
	}
	if len(residuals) != 4 {
		t.Fatalf("residuals = %d, want 4", len(residuals))
	}
}

func TestFitRequiresTwoDistinctPoints(t *testing.T) {
	if _, _, _, _, _, err := Fit([]CalibrationPoint{{Reference: 1, Response: 2}}); err == nil {
		t.Fatal("single point must not calibrate")
	}
	if _, _, _, _, _, err := Fit([]CalibrationPoint{{Reference: 1, Response: 2}, {Reference: 1, Response: 3}}); err == nil {
		t.Fatal("identical reference concentrations must not calibrate")
	}
}

func TestIssueRecordsScopeRangeResidualsValidity(t *testing.T) {
	registry := NewRegistry()
	cert, err := registry.Issue(CertificateInput{
		Component:  "top",
		Points:     idealPoints(),
		ValidFrom:  100,
		ValidUntil: 200,
		IssuedAt:   90,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if cert.Component != "top" {
		t.Fatalf("component = %q", cert.Component)
	}
	if cert.MinConcentration != 0 || cert.MaxConcentration != 3 {
		t.Fatalf("concentration range = %v..%v, want 0..3", cert.MinConcentration, cert.MaxConcentration)
	}
	if cert.MinResponse != 1 || cert.MaxResponse != 7 {
		t.Fatalf("response range = %v..%v, want 1..7", cert.MinResponse, cert.MaxResponse)
	}
	if !cert.EffectiveAt(150) {
		t.Fatal("certificate should be effective inside validity period")
	}
	if cert.EffectiveAt(99) || cert.EffectiveAt(201) {
		t.Fatal("certificate must not be effective outside validity period")
	}
	if cert.Digest == "" {
		t.Fatal("digest missing")
	}
	// 换算关系：浓度 = (响应-1)/2
	if got := cert.Concentration(5); math.Abs(got-2) > 1e-12 {
		t.Fatalf("concentration(response=5) = %v, want 2", got)
	}
}

func TestIssueRejectsBadInput(t *testing.T) {
	registry := NewRegistry()
	cases := []CertificateInput{
		{Component: "", Points: idealPoints(), ValidFrom: 1, ValidUntil: 2},
		{Component: "top", Points: []CalibrationPoint{{Reference: 0, Response: 0}}, ValidFrom: 1, ValidUntil: 2},
		{Component: "top", Points: idealPoints(), ValidFrom: 100, ValidUntil: 100},
		{Component: "top", Points: idealPoints(), ValidFrom: 0, ValidUntil: 100},
		{Component: "top", Points: []CalibrationPoint{{Reference: -1, Response: 0}, {Reference: 1, Response: 2}}, ValidFrom: 1, ValidUntil: 2},
	}
	for i, input := range cases {
		if _, err := registry.Issue(input); err == nil {
			t.Fatalf("case %d should be rejected", i)
		}
	}
}

func TestRevokeKeepsRecordButInvalidates(t *testing.T) {
	registry := NewRegistry()
	cert, _ := registry.Issue(CertificateInput{Component: "top", Points: idealPoints(), ValidFrom: 100, ValidUntil: 200, IssuedAt: 90})
	if _, err := registry.Revoke(cert.ID, 120, "bad standard gas"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, ok := registry.Get(cert.ID)
	if !ok {
		t.Fatal("revoked certificate record must be retained")
	}
	if !got.Revoked || got.RevokeReason != "bad standard gas" {
		t.Fatalf("revocation state wrong: %+v", got)
	}
	if got.EffectiveAt(150) {
		t.Fatal("revoked certificate must not be effective at any time")
	}
	if _, err := registry.Revoke(cert.ID, 121, "again"); err == nil {
		t.Fatal("double revoke must fail")
	}
	if _, err := registry.Revoke([16]byte{}, 1, "x"); err == nil {
		t.Fatal("revoking unknown certificate must fail")
	}
}

func TestEffectivePicksLatestValidForComponent(t *testing.T) {
	registry := NewRegistry()
	c1, _ := registry.Issue(CertificateInput{Component: "top", Points: idealPoints(), ValidFrom: 100, ValidUntil: 200, IssuedAt: 90})
	c2, _ := registry.Issue(CertificateInput{Component: "top", Points: idealPoints(), ValidFrom: 202, ValidUntil: 300, IssuedAt: 200})
	registry.Issue(CertificateInput{Component: "bottom", Points: idealPoints(), ValidFrom: 100, ValidUntil: 300, IssuedAt: 95})

	if got, ok := registry.Effective("top", 150); !ok || got.ID != c1.ID {
		t.Fatalf("at 150 want c1, got %v ok=%v", got.ID, ok)
	}
	if got, ok := registry.Effective("top", 250); !ok || got.ID != c2.ID {
		t.Fatalf("at 250 want c2, got %v", got.ID)
	}
	if _, ok := registry.Effective("top", 201); ok {
		t.Fatal("no certificate effective in the gap")
	}
	if _, ok := registry.Effective("side", 150); ok {
		t.Fatal("component filter must apply")
	}

	registry.Revoke(c2.ID, 210, "x")
	if _, ok := registry.Effective("top", 250); ok {
		t.Fatal("revoked certificate must not resolve")
	}
}

func TestRegistryRestoreRoundTrip(t *testing.T) {
	registry := NewRegistry()
	c1, _ := registry.Issue(CertificateInput{Component: "top", Points: idealPoints(), ValidFrom: 1, ValidUntil: 2, IssuedAt: 1})
	registry.Revoke(c1.ID, 1, "r")
	c2, _ := registry.Issue(CertificateInput{Component: "top", Points: idealPoints(), ValidFrom: 3, ValidUntil: 4, IssuedAt: 3})

	rebuilt := NewRegistry()
	rebuilt.Restore(registry.Snapshot())
	got1, ok := rebuilt.Get(c1.ID)
	if !ok || !got1.Revoked {
		t.Fatal("revoked state must survive restore")
	}
	got2, ok := rebuilt.Get(c2.ID)
	if !ok || math.Abs(got2.Slope-2) > 1e-12 {
		t.Fatal("fit must survive restore")
	}
	if listed := rebuilt.List(); len(listed) != 2 || listed[0].ID != c1.ID {
		t.Fatalf("list order/content wrong: %d", len(listed))
	}
}
