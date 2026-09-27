package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/netcore-isp/netcore/internal/auth"
)

type resetStoreStub struct {
	preview TestDataResetPreview
	executed bool
}

func (s *resetStoreStub) PreviewTestDataReset(context.Context, string) (TestDataResetPreview, error) { return s.preview, nil }
func (s *resetStoreStub) ExecuteTestDataReset(_ context.Context, _, _, backup, reason string) (TestDataResetResult, error) {
	if backup == "" || reason == "" { return TestDataResetResult{}, errors.New("missing reset evidence") }
	s.executed = true
	return TestDataResetResult{RunID: "22222222-2222-4222-8222-222222222222"}, nil
}

type resetStepUpStub struct{ err error }
func (s resetStepUpStub) VerifyStepUp(context.Context, auth.StepUpInput) error { return s.err }

func resetPrincipal() auth.Principal {
	return auth.Principal{TenantID: workspaceTestTenantID, UserID: "22222222-2222-4222-8222-222222222222", Email: "admin@example.test", Permissions: map[string]struct{}{"tenant.test_data_reset": {}}}
}

func TestTestDataResetRequiresExactTenantConfirmationAndFreshStepUp(t *testing.T) {
	store := &resetStoreStub{}
	service, err := NewTestDataResetService(store, resetStepUpStub{})
	if err != nil { t.Fatal(err) }
	_, err = service.Execute(context.Background(), TestDataResetInput{Principal: resetPrincipal(), Password: "current", MFACode: "123456", BackupReference: "backup.dump", Reason: "pre-live reset", Confirmation: "RESET another-tenant"}, "lagos-hub")
	if !errors.Is(err, ErrInvalidReset) || store.executed { t.Fatalf("err=%v executed=%t", err, store.executed) }
	_, err = service.Execute(context.Background(), TestDataResetInput{Principal: resetPrincipal(), Password: "current", MFACode: "123456", BackupReference: "backup.dump", Reason: "pre-live reset", Confirmation: "RESET lagos-hub"}, "lagos-hub")
	if err != nil || !store.executed { t.Fatalf("err=%v executed=%t", err, store.executed) }
}

func TestTestDataResetBlocksActiveNetworkOrQueuedWork(t *testing.T) {
	store := &resetStoreStub{preview: TestDataResetPreview{ActiveSessions: 1}}
	service, _ := NewTestDataResetService(store, resetStepUpStub{})
	_, err := service.Execute(context.Background(), TestDataResetInput{Principal: resetPrincipal(), Password: "current", MFACode: "123456", BackupReference: "backup.dump", Reason: "pre-live reset", Confirmation: "RESET lagos-hub"}, "lagos-hub")
	if !errors.Is(err, ErrResetBlocked) || store.executed { t.Fatalf("err=%v executed=%t", err, store.executed) }
}
