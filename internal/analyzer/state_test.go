package analyzer

import (
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
	"testing"
)

func issueTestCert(t *testing.T, state *State, from, until int64) Certificate {
	t.Helper()
	cert, err := state.IssueCalibration(CertificateInput{
		Component:  "top",
		Points:     idealPoints(),
		ValidFrom:  from,
		ValidUntil: until,
		IssuedAt:   from,
	})
	if err != nil {
		t.Fatalf("issue certificate: %v", err)
	}
	return cert
}

func addSample(t *testing.T, state *State, feed uuid.UUID, response float64, at int64) bool {
	t.Helper()
	return state.Add(model.Sample{ID: uuid.New(), FeedRevision: feed, Component: "top", Value: response, At: at, Valid: true})
}

func TestSampleRejectedWithoutCertificate(t *testing.T) {
	state := New()
	feed := uuid.New()
	state.BindFeed(feed)
	if addSample(t, state, feed, 3, 100) {
		t.Fatal("sample must be rejected before any calibration certificate exists")
	}
}

func TestSampleUsesEffectiveCertificateAndKeepsRawResponse(t *testing.T) {
	state := New()
	issueTestCert(t, state, 100, 200)
	feed := uuid.New()
	state.BindFeed(feed)

	// response 5 -> concentration (5-1)/2 = 2
	if !addSample(t, state, feed, 5, 100) {
		t.Fatal("in-range sample with effective certificate should be accepted")
	}
	samples := state.window.Samples()
	if len(samples) != 1 {
		t.Fatalf("window size = %d", len(samples))
	}
	if samples[0].Value != 5 {
		t.Fatalf("raw response must be preserved, got %v", samples[0].Value)
	}
	if samples[0].Concentration != 2 {
		t.Fatalf("concentration = %v, want 2", samples[0].Concentration)
	}
	if samples[0].Certificate == uuid.Nil {
		t.Fatal("sample must be bound to the effective certificate")
	}
	responses := state.RawResponses()
	if len(responses) != 1 || responses[0].Value != 5 {
		t.Fatalf("raw response ledger wrong: %+v", responses)
	}

	// 超出标定响应覆盖范围 [1,7]
	if addSample(t, state, feed, 100, 101) {
		t.Fatal("response outside calibrated range must be rejected")
	}
	// 证书有效期外
	if addSample(t, state, feed, 5, 201) {
		t.Fatal("measurement after certificate expiry must be rejected")
	}
	if addSample(t, state, feed, 5, 99) {
		t.Fatal("measurement before certificate validity must be rejected")
	}
}

func TestRevocationInvalidatesProofButKeepsResponses(t *testing.T) {
	state := New()
	cert := issueTestCert(t, state, 100, 200)
	feed := uuid.New()
	state.BindFeed(feed)
	if !addSample(t, state, feed, 3, 100) || !addSample(t, state, feed, 3.1, 110) || !addSample(t, state, feed, 2.9, 120) {
		t.Fatal("three samples should complete the window")
	}
	proof, ok := state.Proof(feed)
	if !ok {
		t.Fatal("proof should be available with effective certificate")
	}
	if proof.Source != cert.ID.String() {
		t.Fatalf("proof source = %s, want certificate %s", proof.Source, cert.ID)
	}
	if proof.At != 120 {
		t.Fatalf("proof measurement time = %d, want 120", proof.At)
	}

	// 撤销证书：窗口作废、证明不可再生成
	if _, err := state.RevokeCalibration(cert.ID, 130, "standard gas expired"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if state.Calibration() != uuid.Nil {
		t.Fatal("current certificate must be cleared after revocation")
	}
	if _, ok := state.Proof(feed); ok {
		t.Fatal("proof must not be produced after certificate revocation")
	}
	if got := len(state.RawResponses()); got != 3 {
		t.Fatalf("raw responses must survive revocation, got %d", got)
	}
	if got, _ := state.Registry().Get(cert.ID); !got.Revoked {
		t.Fatal("revoked certificate record must be retained")
	}

	// 已撤销的旧证书不能再用于采样；新证书生效后窗口重新计数
	newCert := issueTestCert(t, state, 140, 300)
	if addSample(t, state, feed, 3, 135) {
		t.Fatal("measurement at 135 must not use a certificate valid only from 140")
	}
	if !addSample(t, state, feed, 3, 140) {
		t.Fatal("measurement under the new certificate should be accepted")
	}
	if state.SampleCount() != 1 {
		t.Fatalf("window must restart after re-calibration, got %d", state.SampleCount())
	}
	if newCert.ID == cert.ID {
		t.Fatal("new certificate must have a distinct identity")
	}
}

func TestEvidenceMarkedStaleOnRevocation(t *testing.T) {
	state := New()
	cert := issueTestCert(t, state, 100, 200)
	feed := uuid.New()
	state.BindFeed(feed)
	addSample(t, state, feed, 3, 100)
	addSample(t, state, feed, 3, 110)
	addSample(t, state, feed, 3, 120)
	proof, _ := state.Proof(feed)

	catalog := NewEvidenceCatalog()
	if err := catalog.Add(proof); err != nil {
		t.Fatalf("add proof: %v", err)
	}
	if marked := catalog.MarkStale(cert.ID.String()); marked != 1 {
		t.Fatalf("marked = %d, want 1", marked)
	}
	stale := catalog.Stale(feed.String())
	if len(stale) != 1 || !stale[0].Stale {
		t.Fatalf("stale proofs wrong: %+v", stale)
	}
	// 证明记录仍在
	if catalog.Count(feed.String()) != 1 {
		t.Fatal("stale marking must not delete proof records")
	}
}

func TestCaptureRestorePreservesCertificatesAndResponses(t *testing.T) {
	state := New()
	cert := issueTestCert(t, state, 100, 200)
	feed := uuid.New()
	state.BindFeed(feed)
	addSample(t, state, feed, 3, 100)
	addSample(t, state, feed, 3, 110)

	recovery := Capture(state)
	if len(recovery.Certificates) != 1 || len(recovery.Responses) != 2 {
		t.Fatalf("recovery capture wrong: certs=%d responses=%d", len(recovery.Certificates), len(recovery.Responses))
	}

	clone := New()
	Restore(clone, recovery)
	got, ok := clone.Registry().Get(cert.ID)
	if !ok || mathAbs(got.Slope-2) > 1e-12 {
		t.Fatal("certificate fit must survive restore")
	}
	if len(clone.RawResponses()) != 2 {
		t.Fatal("raw responses must survive restore")
	}
	if !Compatible(Capture(clone), cert.ID, feed) {
		t.Fatal("restored state should be compatible with current certificate/feed")
	}
	// 再采一条即可出证
	addSample(t, clone, feed, 3, 120)
	if _, ok := clone.Proof(feed); !ok {
		t.Fatal("proof should be available after restore + completing window")
	}
}

func TestRestoreRevokedCertificateLeavesWindowEmpty(t *testing.T) {
	state := New()
	cert := issueTestCert(t, state, 100, 200)
	feed := uuid.New()
	state.BindFeed(feed)
	addSample(t, state, feed, 3, 100)
	addSample(t, state, feed, 3, 110)
	if _, err := state.RevokeCalibration(cert.ID, 130, "x"); err != nil {
		t.Fatal(err)
	}
	recovery := Capture(state)

	clone := New()
	Restore(clone, recovery)
	if clone.Calibration() != uuid.Nil {
		t.Fatal("revoked current certificate must not restore as active")
	}
	if clone.SampleCount() != 0 {
		t.Fatalf("window for revoked certificate must be empty, got %d", clone.SampleCount())
	}
	if len(clone.RawResponses()) != 2 {
		t.Fatal("historical raw responses must still be present")
	}
	if Compatible(recovery, cert.ID, feed) {
		t.Fatal("recovery for revoked certificate must not be compatible")
	}
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
