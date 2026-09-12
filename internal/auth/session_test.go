package auth

import "testing"

func TestNewSessionSecretsAreOpaqueAndVerifiable(t *testing.T) {
	session, rawSession, rawCSRF, err := NewSession("user-id")
	if err != nil {
		t.Fatal(err)
	}
	if rawSession == "" || rawCSRF == "" || session.TokenHash == rawSession || session.CSRFHash == rawCSRF {
		t.Fatal("session secret is empty or persisted raw")
	}
	if !session.ValidCSRF(rawCSRF) || session.ValidCSRF("wrong") {
		t.Fatal("csrf verification failed")
	}
}

func TestSessionRejectsEmptyUser(t *testing.T) {
	if _, _, _, err := NewSession(""); err == nil {
		t.Fatal("empty user id accepted")
	}
}
