package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// The file format. A library in another language reads and writes the same bytes.
const (
	envelopeVersion = 1
	kdfName         = "hkdf-sha256"
	cipherName      = "aes-256-gcm"
	kdfSalt         = "d108/secrets/v1"
	kdfInfo         = "credentials"
	aadPrefix       = "d108\x00secrets\x00"
	keyFileSize     = 32
	keySize         = 32
	nonceSize       = 12
)

// envelope is secrets.enc: the headers in clear, the names and values inside ciphertext.
type envelope struct {
	Version         int    `json:"version"`
	KDF             string `json:"kdf"`
	Cipher          string `json:"cipher"`
	MachineIDSource string `json:"machine_id_source"`
	Nonce           string `json:"nonce"`
	Ciphertext      string `json:"ciphertext"`
}

// deriveKey is HKDF-SHA256 over the key file and the machine id:
// secret = keyFile || NUL || source || NUL || value, salt and info fixed. The source is in
// the secret and in the AAD, so an envelope made on one platform cannot be opened on the
// other even with the same id string.
func deriveKey(keyFile []byte, machine machineID) ([]byte, error) {
	if len(keyFile) != keyFileSize {
		return nil, fmt.Errorf("key file holds %d bytes, not %d", len(keyFile), keyFileSize)
	}
	secret := append(append([]byte{}, keyFile...), []byte("\x00"+machine.Source+"\x00"+machine.Value)...)
	return hkdf.Key(sha256.New, secret, []byte(kdfSalt), kdfInfo, keySize)
}

func aad(source string) []byte { return []byte(aadPrefix + source) }

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal encrypts the names and values into an envelope with a fresh nonce.
func seal(values map[string]string, keyFile []byte, machine machineID) ([]byte, error) {
	plain, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	key, err := deriveKey(keyFile, machine)
	if err != nil {
		return nil, err
	}
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	env := envelope{
		Version:         envelopeVersion,
		KDF:             kdfName,
		Cipher:          cipherName,
		MachineIDSource: machine.Source,
		Nonce:           base64.StdEncoding.EncodeToString(nonce),
		Ciphertext:      base64.StdEncoding.EncodeToString(g.Seal(nil, nonce, plain, aad(machine.Source))),
	}
	data, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// errFormat says the file is not an envelope this version writes; errOpen says the key did
// not open it.
var (
	errFormat = errors.New("not a secrets file of this format")
	errOpen   = errors.New("the key does not open the file")
)

// open decrypts an envelope. The machine id source must match the envelope's: the file was
// made on this platform or it cannot be opened.
func open(data, keyFile []byte, machine machineID) (map[string]string, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("%w: %w", errFormat, err)
	}
	if env.Version != envelopeVersion || env.KDF != kdfName || env.Cipher != cipherName {
		return nil, fmt.Errorf("%w: version %d, %s, %s", errFormat, env.Version, env.KDF, env.Cipher)
	}
	if env.MachineIDSource != machine.Source {
		return nil, fmt.Errorf("%w: made on %s, this is %s", errOpen, env.MachineIDSource, machine.Source)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil || len(nonce) != nonceSize {
		return nil, fmt.Errorf("%w: bad nonce", errFormat)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("%w: bad ciphertext", errFormat)
	}
	key, err := deriveKey(keyFile, machine)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errOpen, err)
	}
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	plain, err := g.Open(nil, nonce, ciphertext, aad(machine.Source))
	if err != nil {
		return nil, errOpen
	}
	values := map[string]string{}
	if err := json.Unmarshal(plain, &values); err != nil {
		return nil, fmt.Errorf("%w: %w", errFormat, err)
	}
	return values, nil
}
