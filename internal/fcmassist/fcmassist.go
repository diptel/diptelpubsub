// Package fcmassist sends Android push notifications through Firebase Cloud Messaging's HTTP v1
// API, using a tenant's own service-account JSON key file (see migrations/0007_fcm.sql —
// apps.fcm_credentials_path) to mint short-lived OAuth2 access tokens itself.
//
// Deliberately hand-rolled against the stdlib (crypto/rsa, net/http) rather than pulling in
// golang.org/x/oauth2/google or the firebase-admin-go SDK: the OAuth2 service-account JWT-bearer
// flow is small and well-specified, and this keeps the dependency footprint the same as the rest
// of the repo (see internal/aiassist for the same approach against the OpenAI API).
package fcmassist

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	fcmScope         = "https://www.googleapis.com/auth/firebase.messaging"
	jwtBearerGrant   = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	tokenLifetime    = time.Hour
	tokenCacheMargin = 60 * time.Second // refresh a bit before actual expiry, never serve a token FCM might reject as stale
	defaultTokenURI  = "https://oauth2.googleapis.com/token"
	fcmSendURLFormat = "https://fcm.googleapis.com/v1/projects/%s/messages:send"
)

// serviceAccountFile is the subset of a Firebase/GCP service-account JSON key file this package
// needs — the file has more fields (private_key_id, auth_uri, …) that are irrelevant here.
type serviceAccountFile struct {
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`
}

type cachedToken struct {
	accessToken string
	projectID   string
	expiresAt   time.Time
}

// Client mints and caches OAuth2 access tokens per credentials file path (so tenants sharing one
// Firebase project/key file share one cached token too) and sends FCM HTTP v1 messages.
type Client struct {
	http *http.Client

	mu    sync.Mutex
	cache map[string]*cachedToken // keyed by the credentials file's absolute path
}

// New returns a ready-to-use Client. There's no "API key" to construct it with — unlike
// aiassist.Client, credentials are per-tenant and read from disk per send (and cached), per
// migrations/0007_fcm.sql.
func New() *Client {
	return &Client{
		http:  &http.Client{Timeout: 15 * time.Second},
		cache: map[string]*cachedToken{},
	}
}

// Priority mirrors FCM's AndroidConfig.priority enum.
type Priority string

const (
	PriorityNormal Priority = "NORMAL"
	PriorityHigh   Priority = "HIGH"
)

// Message is one Android push notification to send through FCM HTTP v1.
type Message struct {
	Token     string   // the device's FCM registration token
	Title     string   // notification.title
	Body      string   // notification.body
	URL       string   // data.url — the app opens this when the notification is tapped (required by contract)
	ChannelID string   // android.notification.channel_id (e.g. "order", "chat", "info")
	Priority  Priority // android.priority; PriorityNormal if empty
}

// Result reports how FCM responded, so the caller can decide whether to prune the token.
type Result struct {
	// Unregistered is true when FCM reports the token itself is gone or malformed
	// (UNREGISTERED or INVALID_ARGUMENT) — the caller should delete it (db.DeleteFCMTokenByID),
	// same as a 404/410 from a Web Push endpoint.
	Unregistered bool
}

// Send delivers one message through FCM HTTP v1, using credentialsPath (apps.fcm_credentials_path
// — an in-container path to a service-account JSON key file) to authenticate.
func (c *Client) Send(ctx context.Context, credentialsPath string, msg Message) (*Result, error) {
	accessToken, projectID, err := c.tokenFor(ctx, credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("fcmassist: failed to get access token: %w", err)
	}

	priority := msg.Priority
	if priority == "" {
		priority = PriorityNormal
	}

	body := map[string]any{
		"message": map[string]any{
			"token": msg.Token,
			"notification": map[string]string{
				"title": msg.Title,
				"body":  msg.Body,
			},
			"data": map[string]string{
				"url": msg.URL,
			},
			"android": map[string]any{
				"priority": string(priority),
				"notification": map[string]string{
					"channel_id": msg.ChannelID,
				},
			},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("fcmassist: failed to encode message: %w", err)
	}

	sendURL := fmt.Sprintf(fcmSendURLFormat, projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fcmassist: request to FCM failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		return &Result{}, nil
	}

	errCode := fcmErrorCode(respBody)
	result := &Result{Unregistered: errCode == "UNREGISTERED" || errCode == "INVALID_ARGUMENT"}
	return result, fmt.Errorf("fcmassist: FCM send failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
}

// fcmErrorResponse is the error shape FCM HTTP v1 returns; the actual per-token reason
// (UNREGISTERED, INVALID_ARGUMENT, ...) lives in details[].errorCode, not the top-level status.
type fcmErrorResponse struct {
	Error struct {
		Status  string `json:"status"`
		Details []struct {
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

func fcmErrorCode(body []byte) string {
	var parsed fcmErrorResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	for _, d := range parsed.Error.Details {
		if d.ErrorCode != "" {
			return d.ErrorCode
		}
	}
	return parsed.Error.Status
}

// tokenFor returns a cached or freshly minted OAuth2 access token (and the service account's
// project id) for the given credentials file.
func (c *Client) tokenFor(ctx context.Context, credentialsPath string) (accessToken, projectID string, err error) {
	c.mu.Lock()
	if cached, ok := c.cache[credentialsPath]; ok && time.Now().Before(cached.expiresAt) {
		accessToken, projectID = cached.accessToken, cached.projectID
		c.mu.Unlock()
		return accessToken, projectID, nil
	}
	c.mu.Unlock()

	sa, err := loadServiceAccount(credentialsPath)
	if err != nil {
		return "", "", err
	}

	jwt, err := signServiceAccountJWT(sa)
	if err != nil {
		return "", "", fmt.Errorf("failed to sign service-account JWT: %w", err)
	}

	tokenURI := sa.TokenURI
	if tokenURI == "" {
		tokenURI = defaultTokenURI
	}
	form := url.Values{
		"grant_type": {jwtBearerGrant},
		"assertion":  {jwt},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return "", "", fmt.Errorf("could not parse token response: %s", strings.TrimSpace(string(respBody)))
	}

	ttl := time.Duration(tokenResp.ExpiresIn) * time.Second
	if ttl <= tokenCacheMargin {
		ttl = tokenLifetime
	}

	c.mu.Lock()
	c.cache[credentialsPath] = &cachedToken{
		accessToken: tokenResp.AccessToken,
		projectID:   sa.ProjectID,
		expiresAt:   time.Now().Add(ttl - tokenCacheMargin),
	}
	c.mu.Unlock()

	return tokenResp.AccessToken, sa.ProjectID, nil
}

func loadServiceAccount(path string) (*serviceAccountFile, error) {
	if path == "" {
		return nil, fmt.Errorf("no credentials file configured")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file %q: %w", path, err)
	}
	var sa serviceAccountFile
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, fmt.Errorf("failed to parse credentials file %q: %w", path, err)
	}
	if sa.ProjectID == "" || sa.PrivateKey == "" || sa.ClientEmail == "" {
		return nil, fmt.Errorf("credentials file %q is missing project_id/private_key/client_email", path)
	}
	return &sa, nil
}

// signServiceAccountJWT builds and RS256-signs the JWT-bearer assertion Google's OAuth2 token
// endpoint expects for a service account (the "two-legged" server-to-server flow — no user
// consent screen, no refresh token).
func signServiceAccountJWT(sa *serviceAccountFile) (string, error) {
	key, err := parsePrivateKey(sa.PrivateKey)
	if err != nil {
		return "", err
	}

	now := time.Now()
	aud := sa.TokenURI
	if aud == "" {
		aud = defaultTokenURI
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   sa.ClientEmail,
		"scope": fcmScope,
		"aud":   aud,
		"iat":   now.Unix(),
		"exp":   now.Add(tokenLifetime).Unix(),
	}

	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)
	signingInput := base64url(headerJSON) + "." + base64url(claimsJSON)

	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func base64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// parsePrivateKey accepts both PKCS#1 ("BEGIN RSA PRIVATE KEY") and PKCS#8 ("BEGIN PRIVATE KEY")
// PEM blocks — Google service-account keys are PKCS#8, but this is cheap to support either way.
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("private_key is not valid PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}
	return rsaKey, nil
}
