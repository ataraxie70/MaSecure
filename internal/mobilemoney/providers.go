package mobilemoney

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type PayoutCommand struct {
	CycleID             string            `json:"cycle_id"`
	GroupID             string            `json:"group_id"`
	BeneficiaryID       string            `json:"beneficiary_id"`
	BeneficiaryMsisdn   string            `json:"beneficiary_msisdn"`
	BeneficiaryProvider string            `json:"beneficiary_provider"`
	AmountMinor         int64             `json:"amount_minor"`
	IdempotencyKey      string            `json:"idempotency_key"`
	InitiatedAt         FlexibleTimestamp `json:"initiated_at"`
}

type FlexibleTimestamp struct {
	raw json.RawMessage
}

func NewFlexibleTimestampString(value string) FlexibleTimestamp {
	raw, _ := json.Marshal(value)
	return FlexibleTimestamp{raw: raw}
}

func (t *FlexibleTimestamp) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		t.raw = json.RawMessage("null")
		return nil
	}

	t.raw = append(t.raw[:0], trimmed...)
	return nil
}

func (t FlexibleTimestamp) MarshalJSON() ([]byte, error) {
	if len(t.raw) == 0 {
		return []byte("null"), nil
	}
	return t.raw, nil
}

func (t FlexibleTimestamp) String() string {
	if len(t.raw) == 0 {
		return ""
	}

	var value string
	if err := json.Unmarshal(t.raw, &value); err == nil {
		return value
	}

	return string(t.raw)
}

type DispatchResult struct {
	ExternalRef        string
	ConfirmImmediately bool
}

type Provider interface {
	Name() string
	SendPayout(ctx context.Context, cmd PayoutCommand) (DispatchResult, error)
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	registry := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		registry.providers[NormalizeProvider(provider.Name())] = provider
	}
	return registry
}

func (r *Registry) Resolve(raw string) (Provider, error) {
	name := NormalizeProvider(raw)
	provider, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("unsupported mobile money provider %q", raw)
	}
	return provider, nil
}

func NormalizeProvider(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")

	switch normalized {
	case "orangemoney", "orange_money":
		return "orange_money"
	case "moovmoney", "moov_money":
		return "moov_money"
	case "wave":
		return "wave"
	default:
		return normalized
	}
}

type MockProvider struct {
	name    string
	latency time.Duration
}

func NewMockProvider(name string, latency time.Duration) *MockProvider {
	return &MockProvider{
		name:    NormalizeProvider(name),
		latency: latency,
	}
}

func (p *MockProvider) Name() string {
	return p.name
}

func (p *MockProvider) SendPayout(ctx context.Context, cmd PayoutCommand) (DispatchResult, error) {
	select {
	case <-ctx.Done():
		return DispatchResult{}, ctx.Err()
	case <-time.After(p.latency):
	}

	return DispatchResult{
		ExternalRef:        "mock-ref-" + cmd.IdempotencyKey,
		ConfirmImmediately: true,
	}, nil
}

type LiveConfig struct {
	// CallbackBaseURL : URL publique de l'API MaSecure (ex: https://api.masecure.bf)
	CallbackBaseURL string
	BaseURL    string
	APIKey     string
	HMACSecret string
}

type LiveHTTPProvider struct {
	name   string
	config LiveConfig
	client HTTPClient
}

type AggregatorPayoutRequest struct {
	Provider string        `json:"provider"`
	Payout   PayoutCommand `json:"payout"`
}

type AggregatorPayoutResponse struct {
	ExternalRef string `json:"external_ref"`
	Status      string `json:"status"`
}

func NewLiveHTTPProvider(name string, config LiveConfig) *LiveHTTPProvider {
	return NewLiveHTTPProviderWithClient(name, config, &http.Client{Timeout: 10 * time.Second})
}

func NewLiveHTTPProviderWithClient(
	name string,
	config LiveConfig,
	client HTTPClient,
) *LiveHTTPProvider {
	return &LiveHTTPProvider{
		name:   NormalizeProvider(name),
		config: config,
		client: client,
	}
}

func (p *LiveHTTPProvider) Name() string {
	return p.name
}

func (p *LiveHTTPProvider) SendPayout(ctx context.Context, cmd PayoutCommand) (DispatchResult, error) {
	if err := p.validate(); err != nil {
		return DispatchResult{}, err
	}

	body, err := p.buildRequestBody(cmd)
	if err != nil {
		return DispatchResult{}, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(p.config.BaseURL, "/")+"/payouts",
		bytes.NewReader(body),
	)
	if err != nil {
		return DispatchResult{}, fmt.Errorf("build payout request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", p.config.APIKey)
	req.Header.Set("X-Provider", p.name)
	req.Header.Set("X-Idempotency-Key", cmd.IdempotencyKey)
	if p.config.HMACSecret != "" {
		req.Header.Set("X-Signature", "sha256="+signPayload(body, p.config.HMACSecret))
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return DispatchResult{}, fmt.Errorf("send payout to aggregator: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return DispatchResult{}, fmt.Errorf("read aggregator response: %w", err)
	}

	if resp.StatusCode >= http.StatusMultipleChoices {
		return DispatchResult{}, fmt.Errorf(
			"aggregator payout rejected provider=%s status=%d body=%s",
			p.name,
			resp.StatusCode,
			strings.TrimSpace(string(raw)),
		)
	}

	var payload AggregatorPayoutResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return DispatchResult{}, fmt.Errorf("decode aggregator response: %w", err)
	}
	if payload.ExternalRef == "" {
		return DispatchResult{}, fmt.Errorf(
			"aggregator response missing external_ref for provider %q",
			p.name,
		)
	}

	return DispatchResult{
		ExternalRef:        payload.ExternalRef,
		ConfirmImmediately: false,
	}, nil
}

func (p *LiveHTTPProvider) validate() error {
	if strings.TrimSpace(p.config.BaseURL) == "" {
		return fmt.Errorf("missing MOBILE_MONEY_AGGREGATOR_URL for provider %q", p.name)
	}
	if strings.TrimSpace(p.config.APIKey) == "" {
		return fmt.Errorf("missing MOBILE_MONEY_API_KEY for provider %q", p.name)
	}
	return nil
}

func (p *LiveHTTPProvider) buildRequestBody(cmd PayoutCommand) ([]byte, error) {
	payload := AggregatorPayoutRequest{
		Provider: p.name,
		Payout:   cmd,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal aggregator payload: %w", err)
	}
	return body, nil
}

func signPayload(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
