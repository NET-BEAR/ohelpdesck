// Package channels owns provider-neutral channel administration primitives.
package channels

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// AESGCMCipher seals channel credentials before persistence. Its encrypted
// representation is nonce || ciphertext; credentials are never serialized in
// a channel response or written to logs.
type AESGCMCipher struct {
	aead  cipher.AEAD
	keyID string
}
type CredentialMetadata struct {
	KeyID   string
	Version int16
}

func NewAESGCMCipher(key []byte) (*AESGCMCipher, error) {
	return NewAESGCMCipherWithKeyID("v1", key)
}
func NewAESGCMCipherWithKeyID(keyID string, key []byte) (*AESGCMCipher, error) {
	if keyID == "" {
		return nil, fmt.Errorf("channel credentials key id is required")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("channel credentials key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("channel credentials cipher setup failed")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("channel credentials cipher setup failed")
	}
	return &AESGCMCipher{aead: aead, keyID: keyID}, nil
}

func (c *AESGCMCipher) NonceSize() int { return c.aead.NonceSize() }

func (c *AESGCMCipher) Seal(channelID string, channelType Type, plain []byte) ([]byte, CredentialMetadata, error) {
	if c == nil || c.aead == nil {
		return nil, CredentialMetadata{}, fmt.Errorf("channel credentials cipher is not configured")
	}
	metadata := CredentialMetadata{KeyID: c.keyID, Version: 1}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, CredentialMetadata{}, fmt.Errorf("channel credential nonce generation failed")
	}
	return c.aead.Seal(nonce, nonce, plain, credentialAAD(channelID, channelType, metadata)), metadata, nil
}

func (c *AESGCMCipher) Open(channelID string, channelType Type, metadata CredentialMetadata, sealed []byte) ([]byte, error) {
	if c == nil || c.aead == nil || metadata.KeyID != c.keyID || metadata.Version != 1 || len(sealed) < c.aead.NonceSize()+c.aead.Overhead() {
		return nil, fmt.Errorf("invalid encrypted channel credentials")
	}
	plain, err := c.aead.Open(nil, sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():], credentialAAD(channelID, channelType, metadata))
	if err != nil {
		return nil, fmt.Errorf("invalid encrypted channel credentials")
	}
	return plain, nil
}

func credentialAAD(channelID string, channelType Type, metadata CredentialMetadata) []byte {
	return []byte(channelID + "\x00" + string(channelType) + "\x00" + metadata.KeyID + "\x00" + fmt.Sprintf("%d", metadata.Version))
}
