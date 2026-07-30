package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const secretKeySize = 32

func loadSecretMasterKey(root string) ([]byte, error) {
	if root == "" {
		return nil, errors.New("secrets root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "master.key")
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != secretKeySize {
			return nil, errors.New("invalid secrets master key")
		}
		_ = os.Chmod(path, 0o600)
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key = make([]byte, secretKeySize)
	if _, err = io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err = os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func encryptSecret(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, g.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return g.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func decryptSecret(key, ciphertext, nonce []byte) ([]byte, error) {
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
