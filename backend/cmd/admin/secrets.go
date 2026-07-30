package main

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"os"
	"path/filepath"
)

func adminMasterKey() ([]byte, error) {
	root := os.Getenv("CALL_RECORDER_SECRETS_ROOT")
	if root == "" {
		root = "/var/lib/call-recorder/secrets"
	}
	b, err := os.ReadFile(filepath.Join(root, "master.key"))
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, errors.New("invalid secrets master key")
	}
	return b, nil
}
func adminDecryptSecret(key, ciphertext, nonce []byte) ([]byte, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	if len(nonce) != g.NonceSize() {
		return nil, errors.New("invalid secret nonce")
	}
	return g.Open(nil, nonce, ciphertext, nil)
}
