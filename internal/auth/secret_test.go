package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return key
}

func testCreds(t *testing.T) Credentials {
	t.Helper()

	return Credentials{
		ClientID:   "SEARCHADS.11111111-2222-3333-4444-555555555555",
		TeamID:     "SEARCHADS.99999999-8888-7777-6666-555555555555",
		KeyID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		PrivateKey: testKey(t),
	}
}

// The signature has to verify against the public half, which is the only thing
// that proves the assertion is actually signed rather than merely well shaped.
func TestClientSecretVerifiesAgainstThePublicKey(t *testing.T) {
	creds := testCreds(t)

	secret, err := ClientSecret(creds, time.Hour)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	parts := strings.Split(secret, ".")
	if len(parts) != 3 {
		t.Fatalf("want three dot-separated parts, got %d", len(parts))
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("ES256 signature must be 64 raw bytes, got %d (asn1 encoding is the usual cause and Apple rejects it)", len(sig))
	}

	digest := sha256Sum([]byte(parts[0] + "." + parts[1]))

	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	if !ecdsa.Verify(&creds.PrivateKey.PublicKey, digest[:], r, s) {
		t.Fatal("signature does not verify against the public key")
	}
}

// sub and iss are different values and swapping them fails with a message that
// names neither, so the mapping is worth pinning down in a test.
func TestClientSecretPutsTheClientIdInSubAndTheTeamIdInIss(t *testing.T) {
	creds := testCreds(t)

	secret, err := ClientSecret(creds, time.Hour)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	parts := strings.Split(secret, ".")

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	decodeJSON(t, parts[0], &header)

	if header.Alg != "ES256" {
		t.Errorf("alg = %q, want ES256", header.Alg)
	}
	if header.Kid != creds.KeyID {
		t.Errorf("kid = %q, want the key id", header.Kid)
	}

	var payload struct {
		Sub string `json:"sub"`
		Iss string `json:"iss"`
		Aud string `json:"aud"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	decodeJSON(t, parts[1], &payload)

	if payload.Sub != creds.ClientID {
		t.Errorf("sub = %q, want the client id", payload.Sub)
	}
	if payload.Iss != creds.TeamID {
		t.Errorf("iss = %q, want the team id", payload.Iss)
	}
	if payload.Aud != Audience {
		t.Errorf("aud = %q, want %q", payload.Aud, Audience)
	}
	if payload.Exp <= payload.Iat {
		t.Error("exp must be after iat")
	}
}

func TestClientSecretRefusesIncompleteCredentials(t *testing.T) {
	full := testCreds(t)

	cases := map[string]Credentials{
		"no client id":   {TeamID: full.TeamID, KeyID: full.KeyID, PrivateKey: full.PrivateKey},
		"no team id":     {ClientID: full.ClientID, KeyID: full.KeyID, PrivateKey: full.PrivateKey},
		"no key id":      {ClientID: full.ClientID, TeamID: full.TeamID, PrivateKey: full.PrivateKey},
		"no private key": {ClientID: full.ClientID, TeamID: full.TeamID, KeyID: full.KeyID},
	}

	for name, creds := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ClientSecret(creds, time.Hour); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}

// openssl writes SEC1 or PKCS#8 depending on flags nobody remembers using, so
// both have to load or the tool rejects a key that is perfectly good.
func TestParsePrivateKeyAcceptsBothOpensslEncodings(t *testing.T) {
	key := testKey(t)

	sec1, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal sec1: %v", err)
	}

	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}

	cases := map[string][]byte{
		"EC PRIVATE KEY": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1}),
		"PRIVATE KEY":    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}),
	}

	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			parsed, err := ParsePrivateKey(encoded)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if parsed.D.Cmp(key.D) != 0 {
				t.Fatal("parsed a different key than was written")
			}
		})
	}
}

func TestParsePrivateKeyRejectsGarbage(t *testing.T) {
	if _, err := ParsePrivateKey([]byte("this is not a pem file")); err == nil {
		t.Fatal("want an error for a non-PEM file")
	}
}

func decodeJSON(t *testing.T, segment string, into any) {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("unmarshal segment: %v", err)
	}
}
