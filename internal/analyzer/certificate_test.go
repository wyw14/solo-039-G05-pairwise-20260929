package analyzer

import (
	"testing"

	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

func TestCertificateBookApplicability(t *testing.T) {
	book := NewCertificateBook()
	cert, err := model.IssueCertificate("top",
		[]model.CalibrationPoint{{Reference: 10, Response: 1}, {Reference: 50, Response: 5}}, 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Issue(cert); err != nil {
		t.Fatal(err)
	}

	if got := book.CurrentID(); got != cert.ID {
		t.Fatal("issued certificate must be current")
	}
	if _, err := book.ApplicableAt("top", 150); err != nil {
		t.Fatalf("certificate should apply within validity: %v", err)
	}
	if _, err := book.ApplicableAt("top", 201); err == nil {
		t.Fatal("expired certificate must not apply")
	}
	if _, err := book.ApplicableAt("side", 150); err == nil {
		t.Fatal("certificate must not apply to another component")
	}

	if _, err := book.Revoke(cert.ID, 160, "bad standard"); err != nil {
		t.Fatal(err)
	}
	if _, err := book.ApplicableAt("top", 161); err == nil {
		t.Fatal("revoked certificate must not apply")
	}
	if book.CurrentID() != uuid.Nil {
		t.Fatal("revoking the current certificate clears the current generation")
	}
	listed := book.List()
	if len(listed) != 1 || listed[0].RevokedAt != 160 {
		t.Fatal("revoked certificate must remain in the book with its revoke marker")
	}
}
