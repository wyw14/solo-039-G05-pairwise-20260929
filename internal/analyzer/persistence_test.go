package analyzer

import (
	"path/filepath"
	"testing"
)

func TestStateFileRoundTrip(t *testing.T) {
	state := New()
	cert := issueTestCert(t, state, 100, 200)

	path := filepath.Join(t.TempDir(), "analyzer.json")
	file := NewStateFile(path)
	if err := file.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}

	recovery, err := file.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recovery.Certificates) != 1 || recovery.Certificates[0].ID != cert.ID {
		t.Fatalf("certificates not persisted: %+v", recovery.Certificates)
	}

	clone := New()
	Restore(clone, recovery)
	if _, ok := clone.Registry().Get(cert.ID); !ok {
		t.Fatal("certificate unavailable after file restore")
	}
}

func TestStateFileMissing(t *testing.T) {
	file := NewStateFile(filepath.Join(t.TempDir(), "absent.json"))
	if _, err := file.Load(); err == nil {
		t.Fatal("missing state file should return an error")
	}
}
