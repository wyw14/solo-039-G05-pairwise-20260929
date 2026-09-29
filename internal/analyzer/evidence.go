package analyzer

import (
	"errors"
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

func (c *EvidenceCatalog) Independent(revision string) []model.Proof {
	c.mu.Lock()
	defer c.mu.Unlock()
	values := c.byRevision[revision]
	seen := make(map[string]struct{})
	out := make([]model.Proof, 0, len(values))
	for _, proof := range values {
		if !proof.Independent {
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

// MarkStale 在某张校准证被撤销时，把所有以该证书为来源的证明标记为失效，
// 表示相应生产证明必须重新评估。标记不删除任何证明记录。
func (c *EvidenceCatalog) MarkStale(source string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	marked := 0
	for revision, proofs := range c.byRevision {
		for i := range proofs {
			if proofs[i].Source == source {
				proofs[i].Stale = true
				marked++
			}
		}
		c.byRevision[revision] = proofs
	}
	return marked
}

// Stale 返回指定修订下依赖已撤销证书、需要重新评估的证明。
func (c *EvidenceCatalog) Stale(revision string) []model.Proof {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.Proof, 0)
	for _, proof := range c.byRevision[revision] {
		if proof.Stale {
			out = append(out, proof)
		}
	}
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
