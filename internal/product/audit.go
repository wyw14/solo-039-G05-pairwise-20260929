package product

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/analyzer"
	"github.com/wyw14/solo-039/internal/model"
)

// certificateFromProof 从分析仪证明来源中取出校准证 ID；非分析仪证书来源返回 Nil。
func certificateFromProof(proof model.Proof) uuid.UUID {
	if id, ok := analyzer.CertificateIDFromSource(proof.Source); ok {
		return id
	}
	return uuid.Nil
}

type Decision struct {
	Plan  model.ProductPlan
	Proof model.Proof
	// Certificate 是出证时适用的标准样校准证 ID。
	Certificate uuid.UUID
	Opened      bool
	// RequiresReevaluation 在证书被撤销后置真：该产品放行证明必须重新评估。
	RequiresReevaluation bool
	ReevaluateReason     string
	At                   time.Time
	Reason               string
}

type Audit struct {
	mu        sync.Mutex
	decisions []Decision
}

func NewAudit() *Audit { return &Audit{decisions: make([]Decision, 0, 32)} }

func (a *Audit) Record(plan model.ProductPlan, proof model.Proof, opened bool, reason string) {
	a.mu.Lock()
	a.decisions = append(a.decisions, Decision{
		Plan:        plan,
		Proof:       proof,
		Certificate: certificateFromProof(proof),
		Opened:      opened,
		At:          time.Now(),
		Reason:      reason,
	})
	a.mu.Unlock()
}

// InvalidateByCertificate 把依据指定校准证作出的全部放行决策标记为需要重新评估，
// 返回受影响的产品代次。原始决策与证明保留，不做物理删除。
func (a *Audit) InvalidateByCertificate(certID uuid.UUID, reason string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	affected := make(map[string]struct{})
	for i := range a.decisions {
		if a.decisions[i].Certificate == certID && a.decisions[i].Opened {
			a.decisions[i].RequiresReevaluation = true
			a.decisions[i].ReevaluateReason = reason
			affected[a.decisions[i].Plan.Revision.String()] = struct{}{}
		}
	}
	out := make([]string, 0, len(affected))
	for revision := range affected {
		out = append(out, revision)
	}
	return out
}

func (a *Audit) Latest() (Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.decisions) == 0 {
		return Decision{}, errors.New("no product decisions")
	}
	return a.decisions[len(a.decisions)-1], nil
}

func (a *Audit) ForRevision(revision string) []Decision {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Decision, 0)
	for _, decision := range a.decisions {
		if decision.Plan.Revision.String() == revision {
			out = append(out, decision)
		}
	}
	return out
}

// RequiringReevaluation 返回全部证书失效、尚待重新评估的放行决策。
func (a *Audit) RequiringReevaluation() []Decision {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Decision, 0)
	for _, decision := range a.decisions {
		if decision.RequiresReevaluation {
			out = append(out, decision)
		}
	}
	return out
}

func (a *Audit) Count() int { a.mu.Lock(); defer a.mu.Unlock(); return len(a.decisions) }
