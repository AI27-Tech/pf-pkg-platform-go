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
	// RSA public exponents are small enough to fit in an int.
	n := bytesToBigInt(b)
	return int(n.Int64())
}

// Ensure rsa.PublicKey is used (it's imported but used in jwt.go).
var _ *rsa.PublicKey
