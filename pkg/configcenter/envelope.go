package configcenter

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	envelopeSchema = "knowledge-core.io/config-envelope/v1"
	keySize        = 32
	maximumContent = 1 << 20
)

type Binding struct {
	Namespace string
	Group     string
	DataID    string
}

type encryptedValue struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type Envelope struct {
	Schema     string         `json:"schema"`
	KeyID      string         `json:"key_id"`
	WrappedKey encryptedValue `json:"wrapped_key"`
	Payload    encryptedValue `json:"payload"`
}

func Encrypt(plaintext, key []byte, keyID string, binding Binding) ([]byte, error) {
	if err := validateCryptoInputs(plaintext, key, keyID, binding); err != nil {
		return nil, err
	}
	dek := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, fmt.Errorf("encrypt configuration: generate data key: %w", err)
	}
	wrapped, err := seal(key, dek, wrapAAD(keyID))
	if err != nil {
		return nil, fmt.Errorf("encrypt configuration data key: %w", err)
	}
	payload, err := seal(dek, plaintext, payloadAAD(keyID, binding))
	if err != nil {
		return nil, fmt.Errorf("encrypt configuration payload: %w", err)
	}
	envelope := Envelope{
		Schema:     envelopeSchema,
		KeyID:      keyID,
		WrappedKey: wrapped,
		Payload:    payload,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode configuration envelope: %w", err)
	}
	return append(encoded, '\n'), nil
}

func Decrypt(encoded, key []byte, expectedKeyID string, binding Binding) ([]byte, error) {
	if len(encoded) == 0 || len(encoded) > maximumContent*2 {
		return nil, errors.New("decrypt configuration: envelope size is invalid")
	}
	if len(key) != keySize {
		return nil, errors.New("decrypt configuration: key must contain 32 bytes")
	}
	if err := binding.Validate(); err != nil {
		return nil, fmt.Errorf("decrypt configuration: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode configuration envelope: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, err
	}
	if envelope.Schema != envelopeSchema {
		return nil, fmt.Errorf("decrypt configuration: unsupported envelope schema %q", envelope.Schema)
	}
	if envelope.KeyID == "" || envelope.KeyID != expectedKeyID {
		return nil, errors.New("decrypt configuration: key identifier does not match the configured key")
	}
	dek, err := open(key, envelope.WrappedKey, wrapAAD(envelope.KeyID))
	if err != nil {
		return nil, fmt.Errorf("decrypt configuration data key: %w", err)
	}
	if len(dek) != keySize {
		return nil, errors.New("decrypt configuration: data key has an invalid size")
	}
	plaintext, err := open(dek, envelope.Payload, payloadAAD(envelope.KeyID, binding))
	if err != nil {
		return nil, fmt.Errorf("decrypt configuration payload: %w", err)
	}
	if len(plaintext) == 0 || len(plaintext) > maximumContent {
		return nil, errors.New("decrypt configuration: plaintext size is invalid")
	}
	return plaintext, nil
}

func ParseKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, errors.New("parse configuration key: value is required")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("parse configuration key: decode base64: %w", err)
	}
	if len(key) != keySize {
		return nil, errors.New("parse configuration key: decoded value must contain 32 bytes")
	}
	return key, nil
}

func (b Binding) Validate() error {
	for name, value := range map[string]string{
		"namespace": b.Namespace,
		"group":     b.Group,
		"data ID":   b.DataID,
	} {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("configuration binding %s is required", name)
		}
		if strings.ContainsAny(value, "\r\n|") {
			return fmt.Errorf("configuration binding %s contains unsupported characters", name)
		}
	}
	return nil
}

func validateCryptoInputs(plaintext, key []byte, keyID string, binding Binding) error {
	if len(plaintext) == 0 || len(plaintext) > maximumContent {
		return errors.New("encrypt configuration: plaintext size is invalid")
	}
	if len(key) != keySize {
		return errors.New("encrypt configuration: key must contain 32 bytes")
	}
	if strings.TrimSpace(keyID) == "" || strings.ContainsAny(keyID, "\r\n|") {
		return errors.New("encrypt configuration: key identifier is invalid")
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("encrypt configuration: %w", err)
	}
	return nil
}

func seal(key, plaintext, additionalData []byte) (encryptedValue, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return encryptedValue{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return encryptedValue{}, fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, additionalData)
	return encryptedValue{
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

func open(key []byte, value encryptedValue, additionalData []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.Strict().DecodeString(value.Nonce)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, errors.New("nonce is invalid")
	}
	ciphertext, err := base64.StdEncoding.Strict().DecodeString(value.Ciphertext)
	if err != nil || len(ciphertext) < gcm.Overhead() {
		return nil, errors.New("ciphertext is invalid")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, errors.New("authentication failed")
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM cipher: %w", err)
	}
	return gcm, nil
}

func wrapAAD(keyID string) []byte {
	return []byte(envelopeSchema + "|keywrap|" + keyID)
}

func payloadAAD(keyID string, binding Binding) []byte {
	return []byte(strings.Join([]string{
		envelopeSchema,
		"payload",
		keyID,
		binding.Namespace,
		binding.Group,
		binding.DataID,
	}, "|"))
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode configuration envelope: multiple JSON values are not allowed")
		}
		return fmt.Errorf("decode configuration envelope: trailing content: %w", err)
	}
	return nil
}
