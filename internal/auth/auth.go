// Package auth implements the admin web panel's login: username/password (bcrypt)
// plus TOTP (RFC 6238, compatible with Google Authenticator, Authy, 1Password, etc).
package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/url"

	"github.com/pquerna/otp/totp"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
)

// HashPassword bcrypt-hashes a plaintext password for storage.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword reports whether password matches the stored bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// NewSessionToken generates a random, URL-safe session token.
func NewSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GenerateTOTPSecret creates a new TOTP secret and its otpauth:// enrollment URI for a user,
// scoped to this PushHub deployment under the given issuer/account label.
func GenerateTOTPSecret(issuer, accountName string) (secret string, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.String(), nil
}

// BuildOTPAuthURL reconstructs the otpauth:// enrollment URI for an already-generated secret,
// e.g. to re-show the QR code if the user refreshes the setup screen before confirming.
// Never call this with a new random secret — only with one already stored via SetAdminTOTPSecret.
func BuildOTPAuthURL(issuer, accountName, secret string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	u := url.URL{
		Scheme:   "otpauth",
		Host:     "totp",
		Path:     "/" + issuer + ":" + accountName,
		RawQuery: v.Encode(),
	}
	return u.String()
}

// ValidateTOTPCode checks a 6-digit code against the secret, per RFC 6238 defaults
// (30s step, SHA1) — this matches what every common authenticator app produces.
func ValidateTOTPCode(secret, code string) bool {
	return totp.Validate(code, secret)
}

// QRCodePNGBase64 renders an otpauth:// URI as a QR code PNG, base64-encoded for direct
// embedding in an HTML <img src="data:image/png;base64,...">.
func QRCodePNGBase64(otpauthURL string) (string, error) {
	png, err := qrcode.Encode(otpauthURL, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	buf.Write(png)
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
