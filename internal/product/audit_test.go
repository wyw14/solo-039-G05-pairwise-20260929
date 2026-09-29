package product

import (
	"testing"

	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/analyzer"
	"github.com/wyw14/solo-039/internal/model"
)

func TestAuditReevaluationOnCertificateRevocation(t *testing.T) {
	audit := NewAudit()
	revision := uuid.New()
	certID := uuid.New()
	plan := model.ProductPlan{Campaign: uuid.New(), Revision: revision, Grade: "baseline", Target: "tank-a"}
	proof := model.Proof{
		ID:          uuid.New(),
		Revision:    revision,
		Source:      analyzer.CertificateSource(certID),
		Digest:      "d",
		Independent: true,
	}
	audit.Record(plan, proof, true, "durable proof accepted")

	if len(audit.RequiringReevaluation()) != 0 {
		t.Fatal("fresh decision must not require re-evaluation")
	}
	affected := audit.InvalidateByCertificate(certID, "standard out of tolerance")
	if len(affected) != 1 || affected[0] != revision.String() {
		t.Fatalf("affected product revisions not reported: %v", affected)
	}
	pending := audit.RequiringReevaluation()
	if len(pending) != 1 || !pending[0].RequiresReevaluation || pending[0].Certificate != certID {
		t.Fatalf("decision not flagged for re-evaluation: %+v", pending)
	}
	// 决策与证明本身保留，不被物理删除。
	if audit.Count() != 1 {
		t.Fatal("revocation must not delete the decision")
	}
	// 另一张证书的撤销不应波及该决策。
	if again := audit.InvalidateByCertificate(uuid.New(), "x"); len(again) != 0 {
		t.Fatalf("unrelated certificate must not affect the decision: %v", again)
	}
}

func TestAuditIgnoresNonCertificateProof(t *testing.T) {
	audit := NewAudit()
	revision := uuid.New()
	plan := model.ProductPlan{Campaign: uuid.New(), Revision: revision, Grade: "g", Target: "t"}
	proof := model.Proof{ID: uuid.New(), Revision: revision, Source: "column-pressure", Digest: "d"}
	audit.Record(plan, proof, true, "ok")
	if got := audit.InvalidateByCertificate(uuid.New(), "x"); len(got) != 0 {
		t.Fatalf("non-analyzer proofs must not be affected: %v", got)
	}
}
