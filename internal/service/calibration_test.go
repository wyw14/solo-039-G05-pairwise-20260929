package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

// TestCalibrationCertificateLifecycle 端到端验证：
// 签发标准样校准证 → 用测量时适用的证书采样放行 → 撤销证书 →
// 产品放行决策标记重新评估、证据失效、原始响应保留 → 快照恢复后状态一致。
func TestCalibrationCertificateLifecycle(t *testing.T) {
	dir := t.TempDir()
	plant := NewPlant(dir)
	ctx := context.Background()
	if _, err := plant.Start(ctx, "baseline", "primary"); err != nil {
		t.Fatalf("start: %v", err)
	}

	now := time.Now().Unix()
	points := []model.CalibrationPoint{{Reference: 10, Response: 1}, {Reference: 30, Response: 3}, {Reference: 50, Response: 5}}
	cert, err := plant.IssueCalibrationCertificate("top", points, now, now+3600)
	if err != nil {
		t.Fatalf("issue certificate: %v", err)
	}
	// 拟合关系为 y=10x：响应 2 -> 浓度 20，且在证上范围 10~50 内。
	for i := 0; i < 3; i++ {
		if _, _, err := plant.AddSample("top", 2, now+int64(i)*10); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	if plant.RawResponseCount() != 3 {
		t.Fatalf("raw responses archived: %d", plant.RawResponseCount())
	}
	if err := plant.RouteProduct("tank-a"); err != nil {
		t.Fatalf("route product: %v", err)
	}
	if pending := plant.PendingReevaluations(); len(pending) != 0 {
		t.Fatalf("fresh routing must not need re-evaluation: %+v", pending)
	}

	// 撤销证书：放行决策与证明必须进入重新评估状态。
	_, affected, err := plant.RevokeCalibrationCertificate(cert.ID, "standard out of tolerance", now+100)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if len(affected) == 0 {
		t.Fatal("revocation must report affected revisions")
	}
	pending := plant.PendingReevaluations()
	if len(pending) != 1 {
		t.Fatalf("one routed decision must require re-evaluation, got %d", len(pending))
	}
	if plant.ProductTargets() != nil && len(plant.ProductTargets()) != 0 {
		t.Fatal("product route backed by a revoked certificate must be closed")
	}
	revision := plant.Campaign.Snapshot().Revision.String()
	if proofs := plant.Evidence.Independent(revision); len(proofs) != 0 {
		t.Fatalf("revoked-certificate proofs must not remain valid evidence: %+v", proofs)
	}
	if invalidated := plant.Evidence.Invalidated(revision); len(invalidated) != 1 {
		t.Fatalf("invalidated proof must remain for traceability: %+v", invalidated)
	}
	if plant.RawResponseCount() != 3 {
		t.Fatal("revocation must not delete raw responses")
	}
	if _, certs := plant.CalibrationCertificates(); len(certs) != 1 {
		t.Fatalf("revoked certificate stays in the book: %d", len(certs))
	}

	// 快照保存/恢复后，撤销状态、待重新评估来自审计（审计为内存态），
	// 这里验证证书撤销状态与原始响应随快照持久化。
	if err := plant.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	snapshot, err := plant.Snapshots.Load()
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if len(snapshot.Analyzer.Certificates) != 1 || snapshot.Analyzer.Certificates[0].RevokedAt == 0 {
		t.Fatal("revoked certificate not persisted")
	}
	if len(snapshot.Analyzer.Samples) != 3 {
		t.Fatalf("raw responses not persisted: %d", len(snapshot.Analyzer.Samples))
	}

	restored := NewPlant(dir)
	if err := restored.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := restored.Snapshot().Analyzer
	if got.Current != uuid.Nil {
		t.Fatal("restored plant must have no current (active) certificate after revocation")
	}
	if len(got.Certificates) != 1 || got.Certificates[0].RevokedAt == 0 {
		t.Fatal("revocation marker not restored")
	}
	if len(got.Samples) != 3 {
		t.Fatalf("raw responses not restored: %d", len(got.Samples))
	}
	if _, _, err := restored.AddSample("top", 2, time.Now().Unix()); err == nil {
		t.Fatal("sampling after restore must be rejected without an applicable certificate")
	}
}
