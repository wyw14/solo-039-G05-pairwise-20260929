package analyzer

import (
	"errors"
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
	"sort"
	"sync"
)

type EvidenceCatalog struct {
	mu         sync.Mutex
	byRevision map[string][]model.Proof
}

func NewEvidenceCatalog() *EvidenceCatalog {
	return &EvidenceCatalog{byRevision: make(map[string][]model.Proof)}
}

func (c *EvidenceCatalog) Add(proof model.Proof) error {
	if proof.Revision.String() == "00000000-0000-0000-0000-000000000000" {
		return errors.New("proof revision is missing")
	}
	if proof.Source == "" || proof.Digest == "" {
		return errors.New("proof content is incomplete")
	}
	c.mu.Lock()
	c.byRevision[proof.Revision.String()] = append(c.byRevision[proof.Revision.String()], proof)
	c.mu.Unlock()
	return nil
}

// Independent 只返回仍站得住的独立证明；证书被撤销而失效的证明不再作为
// 当前证据使用，但仍保留在目录中，可通过 All/Invalidated 追溯并重新评估。
func (c *EvidenceCatalog) Independent(revision string) []model.Proof {
	c.mu.Lock()
	defer c.mu.Unlock()
	values := c.byRevision[revision]
	seen := make(map[string]struct{})
	out := make([]model.Proof, 0, len(values))
	for _, proof := range values {
		if !proof.Independent || proof.Invalidated {
			continue
		}
		if _, ok := seen[proof.Source]; ok {
			continue
		}
		seen[proof.Source] = struct{}{}
		out = append(out, proof)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// All 返回某代次的全部证明（含已失效），供审计。
func (c *EvidenceCatalog) All(revision string) []model.Proof {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]model.Proof(nil), c.byRevision[revision]...)
}

// Invalidated 返回某代次因证书撤销而必须重新评估的证明。
func (c *EvidenceCatalog) Invalidated(revision string) []model.Proof {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.Proof, 0)
	for _, proof := range c.byRevision[revision] {
		if proof.Invalidated {
			out = append(out, proof)
		}
	}
	return out
}

// InvalidateByCertificate 把依据指定校准证出具的全部证明标记为失效，
// 返回受影响的进料/产品代次列表，供上游触发重新评估。记录不删除。
func (c *EvidenceCatalog) InvalidateByCertificate(certID uuid.UUID) []string {
	source := CertificateSource(certID)
	c.mu.Lock()
	defer c.mu.Unlock()
	affected := make(map[string]struct{})
	for revision, proofs := range c.byRevision {
		for i := range proofs {
			if proofs[i].Source == source {
				proofs[i].Invalidated = true
				c.byRevision[revision][i] = proofs[i]
				affected[revision] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(affected))
	for revision := range affected {
		out = append(out, revision)
	}
	sort.Strings(out)
	return out
}

func (c *EvidenceCatalog) ClearExcept(revision string) {
	c.mu.Lock()
	values := append([]model.Proof(nil), c.byRevision[revision]...)
	c.byRevision = map[string][]model.Proof{revision: values}
	c.mu.Unlock()
}

func (c *EvidenceCatalog) Count(revision string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byRevision[revision])
}
