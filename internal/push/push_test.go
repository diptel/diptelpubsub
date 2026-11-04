package push

import (
	"encoding/base64"
	"testing"
)

func TestGenerateVAPIDKeysOrder(t *testing.T) {
	pub, priv, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	p, err := base64.RawURLEncoding.DecodeString(pub)
	if err != nil || len(p) != 65 || p[0] != 4 {
		t.Fatalf("kunci publik harus titik P-256 65 byte, dapat %d byte: %v", len(p), err)
	}
	k, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil || len(k) != 32 {
		t.Fatalf("kunci rahasia harus 32 byte, dapat %d: %v", len(k), err)
	}
}
