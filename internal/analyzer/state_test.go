package analyzer

import (
	"testing"

	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

func issueTestCert(t *testing.T, state *State, issuedAt, expiresAt int64) model.CalibrationCertificate {
	t.Helper()
	// 响应 1~5 线性对应浓度 10~50：y = 10x
	points := []model.CalibrationPoint{{Reference: 10, Response: 1}, {Reference: 30, Response: 3}, {Reference: 50, Response: 5}}
	cert, err := state.IssueCertificate("top", points, issuedAt, expiresAt)
	if err != nil {
		t.Fatalf("issue certificate: %v", err)
	}
	return cert
}

func TestMeasureRejectedWithoutCertificate(t *testing.T) {
	state := New()
	state.BindFeed(uuid.New())
	if _, err := state.Measure("top", 2, 100); err == nil {
		t.Fatal("measurement without an applicable certificate must be rejected")
	}
	// 即使被拒，原始读数仍归档保留。
	if len(state.ArchivedSamples()) != 1 {
		t.Fatal("rejected raw response must still be archived")
	}
}

func TestMeasureUsesCertificateApplicableAtSampleTime(t *testing.T) {
	state := New()
	feed := uuid.New()
	state.BindFeed(feed)
	cert := issueTestCert(t, state, 100, 200)

	// 有效期内：用证上关系换算浓度。
	sample, err := state.Measure("top", 2, 150)
	if err != nil {
		t.Fatalf("valid measurement rejected: %v", err)
	}
	if sample.Calibration != cert.ID || sample.Value != 20 || sample.Response != 2 {
		t.Fatalf("sample not converted with the certificate: %+v", sample)
	}
	if sample.FeedRevision != feed {
		t.Fatal("sample not bound to feed revision")
	}

	// 签发前与过期后的读数都不得当作合格样本。
	if _, err := state.Measure("top", 2, 99); err == nil {
		t.Fatal("measurement before issue must be rejected")
	}
	if _, err := state.Measure("top", 2, 201); err == nil {
		t.Fatal("expired measurement must be rejected")
	}
	// 组分不符。
	if _, err := state.Measure("side", 2, 150); err == nil {
		t.Fatal("measurement of another component must be rejected")
	}
	// 换算结果超出证上浓度范围（响应 6 -> 60，超过最大参考浓度 50）。
	if _, err := state.Measure("top", 6, 150); err == nil {
		t.Fatal("out-of-range conversion must be rejected")
	}
	// 全部读数——包括不合格的——都保留在归档中。
	if len(state.ArchivedSamples()) != 5 {
		t.Fatalf("all raw responses must be retained, got %d", len(state.ArchivedSamples()))
	}
}

func TestProofFailsAfterRevocationAndReevaluation(t *testing.T) {
	state := New()
	state.ConfigureWindow(2, 1000)
	state.BindFeed(uuid.New())
	cert := issueTestCert(t, state, 100, 500)
	revision := uuid.New()

	if _, err := state.Measure("top", 1, 150); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Measure("top", 2, 160); err != nil {
		t.Fatal(err)
	}
	proof, ok := state.ProofAt(revision, 170)
	if !ok {
		t.Fatal("proof expected with valid certificate")
	}
	if proof.Source != CertificateSource(cert.ID) {
		t.Fatalf("proof must cite its certificate: %s", proof.Source)
	}

	// 撤销证书：窗口样本仍在（原始数据不丢），但不得再出证。
	if _, err := state.Book().Revoke(cert.ID, 180, "standard out of tolerance"); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.ProofAt(revision, 181); ok {
		t.Fatal("proof must not be issued once the certificate is revoked")
	}
	if got := len(state.ArchivedSamples()); got != 2 {
		t.Fatalf("revocation must not drop raw responses, got %d", got)
	}

	// 证据目录中依据该证的证明应被标记失效并报出受影响代次。
	catalog := NewEvidenceCatalog()
	if err := catalog.Add(proof); err != nil {
		t.Fatal(err)
	}
	affected := catalog.InvalidateByCertificate(cert.ID)
	if len(affected) != 1 || affected[0] != revision.String() {
		t.Fatalf("affected revisions not reported: %v", affected)
	}
	if len(catalog.Independent(revision.String())) != 0 {
		t.Fatal("invalidated proof must not serve as current evidence")
	}
	if len(catalog.All(revision.String())) != 1 || len(catalog.Invalidated(revision.String())) != 1 {
		t.Fatal("invalidated proof must remain in the catalog for traceability")
	}
}

func TestNewGenerationKeepsRawResponses(t *testing.T) {
	state := New()
	state.ConfigureWindow(2, 1000)
	state.BindFeed(uuid.New())
	first := issueTestCert(t, state, 100, 200)
	if _, err := state.Measure("top", 1, 150); err != nil {
		t.Fatal(err)
	}

	// 签发新一代证书，旧窗口重置，但旧代次原始响应保留。
	second := issueTestCert(t, state, 201, 400)
	if first.ID == second.ID {
		t.Fatal("new certificate must be a new generation")
	}
	if state.SampleCount() != 0 {
		t.Fatal("new generation starts with an empty evidence window")
	}
	archived := state.ArchivedSamples()
	if len(archived) != 1 || archived[0].Calibration != first.ID {
		t.Fatalf("previous-generation raw response lost: %+v", archived)
	}
	if _, err := state.Measure("top", 2, 250); err != nil {
		t.Fatal(err)
	}
	if len(state.ArchivedSamples()) != 2 {
		t.Fatal("responses of both generations must be retained")
	}
}

func TestCaptureRestoreRebuildsWindowAndRevocation(t *testing.T) {
	state := New()
	state.ConfigureWindow(2, 1000)
	state.BindFeed(uuid.New())
	cert := issueTestCert(t, state, 100, 500)
	if _, err := state.Measure("top", 1, 150); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Measure("top", 2, 160); err != nil {
		t.Fatal(err)
	}

	recovered := Capture(state)
	clone := New()
	Restore(clone, recovered)
	revision := uuid.New()
	if _, ok := clone.ProofAt(revision, 170); !ok {
		t.Fatal("proof should survive capture/restore")
	}

	// 撤销状态与原始归档随恢复保留。
	if _, err := clone.Book().Revoke(cert.ID, 180, "x"); err != nil {
		t.Fatal(err)
	}
	recovered2 := Capture(clone)
	clone2 := New()
	Restore(clone2, recovered2)
	if _, ok := clone2.ProofAt(revision, 181); ok {
		t.Fatal("revocation must survive capture/restore")
	}
	if len(clone2.ArchivedSamples()) != 2 {
		t.Fatal("archive must survive capture/restore")
	}
}
