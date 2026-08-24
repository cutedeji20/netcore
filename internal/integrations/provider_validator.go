package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

const providerValidationResponseLimit = 64 * 1024

// HTTPProviderValidator proves a candidate provider credential works before
// it is encrypted and marked active. It never logs or stores credential data.
type HTTPProviderValidator struct {
	client          *http.Client
	resendBaseURL   string
	paystackBaseURL string
}

func NewHTTPProviderValidator(client *http.Client) (*HTTPProviderValidator, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if client.Timeout <= 0 {
		return nil, errors.New("integrations: provider validation HTTP client requires a timeout")
	}
	return &HTTPProviderValidator{client: client, resendBaseURL: "https://api.resend.com", paystackBaseURL: "https://api.paystack.co"}, nil
}

func (v *HTTPProviderValidator) Validate(ctx context.Context, input ConfigureInput) error {
	if v == nil || v.client == nil || len(input.Credential) == 0 {
		return ErrCredentialInvalid
	}
	switch input.Provider {
	case ProviderResend:
		return v.validateResend(ctx, input)
	case ProviderPaystack:
		return v.validatePaystack(ctx, input)
	default:
		return ErrCredentialInvalid
	}
}

func (v *HTTPProviderValidator) validateResend(ctx context.Context, input ConfigureInput) error {
	sender := strings.TrimSpace(input.SenderEmail)
	destination := strings.TrimSpace(input.Principal.Email)
	parsedSender, senderErr := mail.ParseAddress(sender)
	parsedDestination, destinationErr := mail.ParseAddress(destination)
	if senderErr != nil || parsedSender.Address == "" || destinationErr != nil || parsedDestination.Address != destination {
		return ErrCredentialInvalid
	}
	payload, err := json.Marshal(struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}{
		From: sender, To: destination, Subject: "NetCore Resend configuration verified",
		Text: "This confirms that NetCore can send customer account emails from the configured sender.",
	})
	if err != nil {
		return ErrCredentialInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(v.resendBaseURL, "/")+"/emails", bytes.NewReader(payload))
	if err != nil {
		return ErrCredentialInvalid
	}
	request.Header.Set("Authorization", "Bearer "+string(input.Credential))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "netcore-integration-validator/1.0")
	response, err := v.client.Do(request)
	if err != nil {
		return ErrCredentialInvalid
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ErrCredentialInvalid
	}
	return nil
}

func (v *HTTPProviderValidator) validatePaystack(ctx context.Context, input ConfigureInput) error {
	if mode := strings.ToUpper(strings.TrimSpace(input.PaystackMode)); mode != "TEST" && mode != "LIVE" {
		return ErrCredentialInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(v.paystackBaseURL, "/")+"/balance", nil)
	if err != nil {
		return ErrCredentialInvalid
	}
	request.Header.Set("Authorization", "Bearer "+string(input.Credential))
	request.Header.Set("Accept", "application/json")
	response, err := v.client.Do(request)
	if err != nil {
		return ErrCredentialInvalid
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, providerValidationResponseLimit+1))
	if err != nil || len(body) == 0 || len(body) > providerValidationResponseLimit || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ErrCredentialInvalid
	}
	var payload struct {
		Status bool `json:"status"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || !payload.Status {
		return ErrCredentialInvalid
	}
	return nil
}
