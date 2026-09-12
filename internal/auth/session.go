package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"time"
)

type Session struct {
	UserID    string
	TokenHash string
	CSRFHash  string
	ExpiresAt time.Time
}

func NewSession(userID string) (Session, string, string, error) {
	if userID == "" {
		return Session{}, "", "", fmt.Errorf("user id required")
	}
	token, err := randomToken()
	if err != nil {
		return Session{}, "", "", err
	}
	csrf, err := randomToken()
	if err != nil {
		return Session{}, "", "", err
	}
	return Session{UserID: userID, TokenHash: HashSecret(token), CSRFHash: HashSecret(csrf), ExpiresAt: time.Now().UTC().Add(12 * time.Hour)}, token, csrf, nil
}

func HashSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawStdEncoding.EncodeToString(sum[:])
}

func (s Session) ValidCSRF(raw string) bool {
	return subtle.ConstantTimeCompare([]byte(s.CSRFHash), []byte(HashSecret(raw))) == 1
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
