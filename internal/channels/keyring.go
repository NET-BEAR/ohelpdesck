package channels

import "fmt"

// Keyring retains the active encryption key and explicitly configured prior
// keys for reading legacy ciphertext during a controlled rotation.
type Keyring struct {
	active *AESGCMCipher
	keys   map[string]*AESGCMCipher
}

func NewKeyring(activeID string, activeKey []byte, previous map[string][]byte) (*Keyring, error) {
	active, err := NewAESGCMCipherWithKeyID(activeID, activeKey)
	if err != nil {
		return nil, err
	}
	k := &Keyring{active: active, keys: map[string]*AESGCMCipher{activeID: active}}
	for id, key := range previous {
		if id == activeID {
			return nil, fmt.Errorf("duplicate active credential key id")
		}
		c, e := NewAESGCMCipherWithKeyID(id, key)
		if e != nil {
			return nil, e
		}
		k.keys[id] = c
	}
	return k, nil
}
func (k *Keyring) Seal(id string, typ Type, plain []byte) ([]byte, CredentialMetadata, error) {
	if k == nil || k.active == nil {
		return nil, CredentialMetadata{}, fmt.Errorf("channel credential keyring unavailable")
	}
	return k.active.Seal(id, typ, plain)
}
func (k *Keyring) Open(id string, typ Type, metadata CredentialMetadata, sealed []byte) ([]byte, error) {
	if k == nil || k.keys[metadata.KeyID] == nil {
		return nil, fmt.Errorf("unknown channel credential key")
	}
	return k.keys[metadata.KeyID].Open(id, typ, metadata, sealed)
}
