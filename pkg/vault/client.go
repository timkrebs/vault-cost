package vault

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/timkrebs/vault-cost/pkg/config"
)

// Client talks to a Vault Enterprise server: it authenticates (Kubernetes auth
// preferred, static token fallback) and reads the Activity Export API. Tokens
// are cached in-process and never logged.
type Client struct {
	cfg *config.Config
	hc  *http.Client
	rl  *rate.Limiter

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewClient builds a Vault client from config.
func NewClient(cfg *config.Config) (*Client, error) {
	tr := &http.Transport{}
	if cfg.InsecureSkipVerify {
		// Dev/test only; do not use against a TLS-enabled Vault in production.
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		cfg: cfg,
		hc: &http.Client{
			Timeout:   time.Duration(cfg.RequestTimeoutSec) * time.Second,
			Transport: tr,
		},
		rl: rate.NewLimiter(rate.Limit(cfg.ExportRatePerSec), cfg.ExportRateBurst),
	}, nil
}

// getToken returns a valid Vault token, authenticating or refreshing as needed.
func (c *Client) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cfg.AuthMethod == config.AuthToken {
		return c.cfg.Token, nil
	}
	// Kubernetes auth: reuse the cached token until close to expiry.
	if c.token != "" && time.Now().Before(c.expiry.Add(-60*time.Second)) {
		return c.token, nil
	}
	return c.k8sLogin(ctx)
}

// k8sLogin exchanges the pod ServiceAccount JWT for a short-TTL Vault token.
// Caller must hold c.mu.
func (c *Client) k8sLogin(ctx context.Context) (string, error) {
	jwt, err := os.ReadFile(c.cfg.ServiceTokenPath)
	if err != nil {
		return "", fmt.Errorf("reading service account token: %w", err)
	}
	body, _ := json.Marshal(map[string]string{
		"role": c.cfg.KubernetesRole,
		"jwt":  strings.TrimSpace(string(jwt)),
	})
	endpoint := fmt.Sprintf("%s/v1/auth/%s/login",
		strings.TrimRight(c.cfg.VaultAddress, "/"), c.cfg.KubernetesMount)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("kubernetes login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Body may contain error detail but never a usable secret.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("kubernetes login failed: status %d: %s", resp.StatusCode, string(b))
	}
	var lr struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", fmt.Errorf("decoding login response: %w", err)
	}
	if lr.Auth.ClientToken == "" {
		return "", fmt.Errorf("kubernetes login returned empty token")
	}
	c.token = lr.Auth.ClientToken
	ttl := time.Duration(lr.Auth.LeaseDuration) * time.Second
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	c.expiry = time.Now().Add(ttl)
	return c.token, nil
}

// ExportClients calls the Activity Export API for [start,end) and returns the
// deduplicated-by-caller client records. HTTP 204 (no content) means zero
// clients for the window and returns (nil, nil) - not an error.
func (c *Client) ExportClients(ctx context.Context, start, end time.Time) ([]ClientRecord, error) {
	if err := c.rl.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	tok, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("start_time", start.UTC().Format(time.RFC3339))
	q.Set("end_time", end.UTC().Format(time.RFC3339))
	q.Set("format", "json")
	endpoint := fmt.Sprintf("%s/v1/sys/internal/counters/activity/export?%s",
		strings.TrimRight(c.cfg.VaultAddress, "/"), q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", tok)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("activity export request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent: // 204 => zero records
		return nil, nil
	case http.StatusOK:
		return ParseNDJSON(resp.Body)
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("activity export status %d: %s", resp.StatusCode, string(b))
	}
}
