package telemetry

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Secrets хранит креды только в шифротексте; ключ задаёт оператор отдельно от БД.
type Secrets struct{ Key string }

func (s Secrets) codec() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(s.Key)
	if err != nil || len(key) != 32 {
		return nil, domain.NewError(domain.Invalid, "Для секретов нужен INTEGRATION_SECRET_KEY: 32 байта в base64")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s Secrets) Seal(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	a, err := s.codec()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(a.Seal(nonce, nonce, []byte(value), nil)), nil
}
func (s Secrets) Open(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	a, err := s.codec()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) < a.NonceSize() {
		return "", domain.NewError(domain.Invalid, "Повреждён секрет источника")
	}
	plain, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], nil)
	if err != nil {
		return "", domain.NewError(domain.Invalid, "Не удалось расшифровать секрет источника")
	}
	return string(plain), nil
}
