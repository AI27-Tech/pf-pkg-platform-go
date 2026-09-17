package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"math/big"
)

func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func bytesToBigInt(b []byte) *big.Int {
	return new(big.Int).SetBytes(b)
}

func bytesToInt(b []byte) int {
	// Los exponentes públicos RSA son pequeños y caben en un int.
	n := bytesToBigInt(b)
	return int(n.Int64())
}

// Garantiza que rsa.PublicKey se considere usado (se usa en jwt.go).
var _ *rsa.PublicKey
