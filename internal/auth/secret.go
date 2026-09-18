// Package auth signs the client secret Apple Ads wants and exchanges it for an
// access token.
//
// Apple's flow is the friendliest of any advertising platform: no browser, no
// refresh token, no consent screen that expires while you sleep. You generate
// an EC P-256 key pair once, paste the public half into the Apple Ads UI, and
// from then on a signed assertion is the whole credential.
//
//	openssl ecparam -genkey -name prime256v1 -noout -out private-key.pem
//	openssl ec -in private-key.pem -pubout -out public-key.pem
//
// That makes the tool genuinely headless, which is the point.
package auth

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Audience is the only value Apple accepts in the assertion's `aud`. It is
// appleid.apple.com even though every later request goes to api.ads.apple.com,
// which is the kind of detail that costs an afternoon if you assume otherwise.
const Audience = "https://appleid.apple.com"

// Credentials are the four things the Apple Ads UI hands back after you upload
// a public key, plus the private half you kept.
type Credentials struct {
	ClientID   string
	TeamID     string
	KeyID      string
	PrivateKey *ecdsa.PrivateKey
}

// ParsePrivateKey reads the PEM produced by the openssl command above.
//
// Both the SEC1 ("EC PRIVATE KEY") and PKCS#8 ("PRIVATE KEY") encodings are
// accepted, because which one openssl writes depends on the flags somebody used
// a year ago and a tool that only reads one of them fails with "invalid key" on
// a key that is perfectly good.
func ParsePrivateKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found: expected a file starting with -----BEGIN")
	}

	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		// Apple's own instructions produce a P-256 EC key. An RSA key here is
		// almost always an App Store Connect key, which is a different
		// credential for a different API, and saying so beats "type mismatch".
		return nil, fmt.Errorf("expected an EC private key, got %T: an App Store Connect .p8 is not an Apple Ads key", parsed)
	}

	return key, nil
}

// ClientSecret signs the ES256 assertion Apple takes in place of a password.
//
// Apple's own sample sets the expiry 180 days out. That is allowed and it is
// also a 180-day bearer credential sitting in your shell history, so the
// default here is deliberately shorter and the caller has to ask for more.
func ClientSecret(creds Credentials, lifetime time.Duration) (string, error) {
	if creds.ClientID == "" || creds.TeamID == "" || creds.KeyID == "" {
		return "", errors.New("client id, team id and key id are all required")
	}
	if creds.PrivateKey == nil {
		return "", errors.New("private key is required")
	}

	now := time.Now()

	header, err := json.Marshal(map[string]string{
		"alg": "ES256",
		"kid": creds.KeyID,
	})
	if err != nil {
		return "", err
	}

	// `sub` is the client id and `iss` is the team id. They are different
	// values and swapping them yields a 400 that says only "invalid_client".
	payload, err := json.Marshal(map[string]any{
		"sub": creds.ClientID,
		"iss": creds.TeamID,
		"aud": Audience,
		"iat": now.Unix(),
		"exp": now.Add(lifetime).Unix(),
	})
	if err != nil {
		return "", err
	}

	signing := encode(header) + "." + encode(payload)

	digest := sha256.Sum256([]byte(signing))

	r, s, err := ecdsa.Sign(rand.Reader, creds.PrivateKey, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign assertion: %w", err)
	}

	// JWS wants the raw R||S pair, each left-padded to the curve size. Go's
	// asn1 encoding is a different thing entirely and Apple rejects it, which
	// is the single most common way a hand-rolled ES256 signer is wrong.
	return signing + "." + encode(pair(r, s, creds.PrivateKey.Curve.Params().BitSize)), nil
}

func pair(r, s *big.Int, bits int) []byte {
	size := (bits + 7) / 8
	out := make([]byte, 2*size)
	r.FillBytes(out[:size])
	s.FillBytes(out[size:])

	return out
}

func encode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
