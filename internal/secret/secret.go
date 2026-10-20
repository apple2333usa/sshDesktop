// Package secret 用主密钥（secret.key）以 AES-256-GCM 加解密敏感数据，例如远程节点的 SSH 凭据。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
)

type Box struct{ aead cipher.AEAD }

// Load 读取 32 字节主密钥。
func Load(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取主密钥失败: %w", err)
	}
	return New(key)
}

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("主密钥长度必须是 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal 加密，输出 nonce + 密文。空输入返回空。
func (b *Box) Seal(plain []byte) []byte {
	if len(plain) == 0 {
		return nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	rand.Read(nonce)
	return b.aead.Seal(nonce, nonce, plain, nil)
}

func (b *Box) Open(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	n := b.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("密文损坏")
	}
	out, err := b.aead.Open(nil, data[:n], data[n:], nil)
	if err != nil {
		return nil, errors.New("解密失败：主密钥与数据不匹配")
	}
	return out, nil
}
