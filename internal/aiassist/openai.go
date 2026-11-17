// Package aiassist calls OpenAI's Chat Completions API for the CS Hub AI assistant ("AiSyah" by
// default — see AI_ASSISTANT_NAME). It knows nothing about CS sessions or WhatsApp; callers in
// internal/handlers build the system prompt and message history and hand them to Reply.
package aiassist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Message is one turn of conversation sent to the model. Role is "system", "user" (the
// customer), or "assistant" (a previous AI reply).
type Message struct {
	Role    string
	Content string
}

// Client calls the OpenAI Chat Completions API with a fixed API key and model.
type Client struct {
	apiKey string
	model  string
	http   *http.Client
}

// New builds a Client. apiKey empty is valid — Enabled() reports false and Reply refuses to
// call out — so callers can construct this unconditionally from config without an extra nil
// check at startup.
func New(apiKey, model string) *Client {
	return &Client{apiKey: apiKey, model: model, http: &http.Client{Timeout: 45 * time.Second}}
}

// Enabled reports whether an API key is configured. nil-safe so callers can check
// `client.Enabled()` without first checking `client != nil`.
func (c *Client) Enabled() bool {
	return c != nil && c.apiKey != ""
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Reply sends systemPrompt plus history (oldest first, ending with the customer's latest
// message) to the model and returns its reply text. Returns an error — never a partial/empty
// string silently — on any network, HTTP, or OpenAI-side failure so callers can decide whether
// to fall back to staying silent (leaving the message for a human) rather than sending garbage.
func (c *Client) Reply(ctx context.Context, systemPrompt string, history []Message) (string, error) {
	if !c.Enabled() {
		return "", errors.New("aiassist: OPENAI_API_KEY not configured")
	}

	msgs := make([]chatMessage, 0, len(history)+1)
	msgs = append(msgs, chatMessage{Role: "system", Content: systemPrompt})
	for _, m := range history {
		msgs = append(msgs, chatMessage{Role: m.Role, Content: m.Content})
	}

	reqBody, err := json.Marshal(chatRequest{Model: c.model, Messages: msgs, Temperature: 0.4, MaxTokens: 500})
	if err != nil {
		return "", fmt.Errorf("aiassist: failed to encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("aiassist: failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("aiassist: request to OpenAI failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("aiassist: failed to read OpenAI response: %w", err)
	}

	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("aiassist: failed to parse OpenAI response (status %d): %w", resp.StatusCode, err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("aiassist: OpenAI error: %s", out.Error.Message)
	}
	if resp.StatusCode >= 300 || len(out.Choices) == 0 {
		return "", fmt.Errorf("aiassist: OpenAI returned status %d with no reply", resp.StatusCode)
	}
	return out.Choices[0].Message.Content, nil
}
