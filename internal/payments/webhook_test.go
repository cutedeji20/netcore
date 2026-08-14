package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type memoryWebhookStore struct {
	recorded  []WebhookReceipt
	next      QueuedWebhook
	hasNext   bool
	owner     WebhookPaymentOwner
	hasOwner  bool
	claimErr  error
	processID string
	ignored   bool
	reason    string
	failed    error
}

func (s *memoryWebhookStore) RecordWebhook(_ context.Context, receipt WebhookReceipt) (WebhookRecordResult, error) {
	s.recorded = append(s.recorded, receipt)
	return WebhookRecordResult{}, nil
}
func (s *memoryWebhookStore) ClaimWebhook(_ context.Context, _ string, _ int) (QueuedWebhook, bool, error) {
	if s.claimErr != nil {
		return QueuedWebhook{}, false, s.claimErr
	}
	if !s.hasNext {
		return QueuedWebhook{}, false, nil
	}
	s.hasNext = false
	return s.next, true, nil
}
func (s *memoryWebhookStore) MarkWebhookProcessed(_ context.Context, id string, ignored bool, reason string) error {
	s.processID, s.ignored, s.reason = id, ignored, reason
	return nil
}
func (s *memoryWebhookStore) MarkWebhookFailed(_ context.Context, _ QueuedWebhook, err error) error {
	s.failed = err
	return nil
}
func (s *memoryWebhookStore) PaymentOwnerForWebhook(context.Context, string, string) (WebhookPaymentOwner, bool, error) {
	return s.owner, s.hasOwner, nil
}

func TestWebhookHTTPAcknowledgesOnlyValidSignedRawBody(t *testing.T) {
	secret := "sk_test_not_a_real_secret"
	gateway, err := NewPaystackGateway(paymentSecrets{"payments.paystack.secret_key": secret}, "payments.paystack.secret_key", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryWebhookStore{}
	h, err := NewWebhookHTTP(gateway, store, 1024)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"event":"charge.success","data":{"id":123456789,"reference":"pay-1234567890abcdef1234567890abcdef"}}`)
	mac := hmac.New(sha512.New, []byte(secret))
	_, _ = mac.Write(raw)

	request := httptest.NewRequest(http.MethodPost, "/webhooks/paystack", strings.NewReader(string(raw)))
	request.Header.Set("X-Paystack-Signature", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	h.receive(response, request)
	if response.Code != http.StatusOK || len(store.recorded) != 1 || len(store.recorded[0].PayloadHash) != 32 {
		t.Fatalf("status=%d recorded=%+v", response.Code, store.recorded)
	}

	request = httptest.NewRequest(http.MethodPost, "/webhooks/paystack", strings.NewReader(string(raw)))
	request.Header.Set("X-Paystack-Signature", "bad")
	response = httptest.NewRecorder()
	h.receive(response, request)
	if response.Code != http.StatusUnauthorized || len(store.recorded) != 1 {
		t.Fatalf("invalid signature status=%d recorded=%d", response.Code, len(store.recorded))
	}
}

func TestWebhookProcessorVerifiesBeforeActivation(t *testing.T) {
	reference := "pay-1234567890abcdef1234567890abcdef"
	paymentStore := &memoryPaymentStore{payment: Payment{ID: "payment", SubscriptionID: "subscription", Gateway: "paystack", Reference: reference, AmountMinor: 500000, Currency: "NGN", Status: StatusPending}}
	gateway := &memoryGateway{available: true, verify: GatewayVerification{Reference: reference, Status: StatusSuccess, AmountMinor: 500000, Currency: "NGN", VerifiedAt: time.Now().UTC()}}
	service := newPaymentService(t, paymentStore, gateway)
	queue := &memoryWebhookStore{
		next:     QueuedWebhook{ID: "11111111-1111-4111-8111-111111111111", Provider: "paystack", EventType: paystackChargeSuccess, Reference: reference, Attempts: 1},
		hasNext:  true,
		owner:    WebhookPaymentOwner{TenantID: paymentTestTenant, UserID: paymentTestUser},
		hasOwner: true,
	}
	processor, err := NewWebhookProcessor(queue, service, "paystack", 8)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || gateway.verifyCalls != 1 || paymentStore.activateCalls != 1 || queue.processID == "" || queue.ignored {
		t.Fatalf("worked=%v err=%v verify=%d activate=%d complete=%q ignored=%v", worked, err, gateway.verifyCalls, paymentStore.activateCalls, queue.processID, queue.ignored)
	}
}

func TestWebhookProcessorDoesNotActivateUnknownOrUnverifiedEvents(t *testing.T) {
	reference := "pay-1234567890abcdef1234567890abcdef"
	paymentStore := &memoryPaymentStore{payment: Payment{Gateway: "paystack", Reference: reference, AmountMinor: 500000, Currency: "NGN", Status: StatusPending}}
	gateway := &memoryGateway{available: true, verify: GatewayVerification{Reference: reference, Status: StatusSuccess, AmountMinor: 1, Currency: "NGN", VerifiedAt: time.Now().UTC()}}
	service := newPaymentService(t, paymentStore, gateway)
	queue := &memoryWebhookStore{next: QueuedWebhook{ID: "11111111-1111-4111-8111-111111111111", Provider: "paystack", EventType: paystackChargeSuccess, Reference: reference, Attempts: 1}, hasNext: true, owner: WebhookPaymentOwner{TenantID: paymentTestTenant, UserID: paymentTestUser}, hasOwner: true}
	processor, err := NewWebhookProcessor(queue, service, "paystack", 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessOne(context.Background()); err != nil || !queue.ignored || paymentStore.activateCalls != 0 {
		t.Fatalf("err=%v ignored=%v activate=%d", err, queue.ignored, paymentStore.activateCalls)
	}

	queue.hasNext = true
	queue.hasOwner = false
	queue.processID, queue.ignored = "", false
	if _, err := processor.ProcessOne(context.Background()); err != nil || !queue.ignored || paymentStore.activateCalls != 0 {
		t.Fatalf("unknown event err=%v ignored=%v activate=%d", err, queue.ignored, paymentStore.activateCalls)
	}
}

func TestWebhookRetryHelpersBoundAndRedact(t *testing.T) {
	if got := webhookRetryDelay(20); got != 300*time.Second {
		t.Fatalf("delay=%v", got)
	}
	message := safeWebhookError(errors.New("line one\n" + strings.Repeat("x", 300)))
	if strings.Contains(message, "\n") || len(message) > 240 {
		t.Fatalf("unsafe message=%q", message)
	}
}
