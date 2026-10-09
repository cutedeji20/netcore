package vouchers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

func decodeVoucherMutation(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return false
	}
	return errors.Is(d.Decode(&struct{}{}), io.EOF)
}

func (h *HTTP) issueBatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	var in struct {
		PlanID    string     `json:"plan_id"`
		Quantity  int        `json:"quantity"`
		ExpiresAt *time.Time `json:"expires_at"`
		Reason    string     `json:"reason"`
		Password  string     `json:"password"`
		MFACode   string     `json:"mfa_code"`
	}
	if !decodeVoucherMutation(w, r, &in) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Voucher issuance request is invalid.")
		return
	}
	batch, err := h.staff.Issue(r.Context(), p, in.PlanID, in.Quantity, in.ExpiresAt, in.Reason, in.Password, in.MFACode)
	if errors.Is(err, ErrStaffVoucherDenied) || errors.Is(err, ErrIssueInvalid) {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "Voucher issuance request is not allowed.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "VOUCHERS_UNAVAILABLE", "Voucher issuance is unavailable.")
		return
	}
	writeJSON(w, http.StatusCreated, responseBatch(batch))
}

func (h *HTTP) revokeVoucher(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	var in struct {
		Password string `json:"password"`
		MFACode  string `json:"mfa_code"`
	}
	if !decodeVoucherMutation(w, r, &in) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Voucher revocation request is invalid.")
		return
	}
	err := h.staff.Revoke(r.Context(), p, r.PathValue("id"), in.Password, in.MFACode)
	if errors.Is(err, ErrStaffVoucherDenied) {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "Voucher revocation is not allowed.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "VOUCHERS_UNAVAILABLE", "Voucher revocation is unavailable.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
