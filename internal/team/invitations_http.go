package team

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

const maxInvitationBodyBytes = 32 * 1024

type invitationResponse struct {
	Invitation invitationProjectionResponse `json:"invitation"`
}
type invitationProjectionResponse struct {
	ID        string      `json:"id"`
	Email     string      `json:"email"`
	Role      BuiltInRole `json:"role"`
	Status    string      `json:"status"`
	ExpiresAt time.Time   `json:"expires_at"`
}

func invitationProjection(inv Invitation) invitationProjectionResponse {
	return invitationProjectionResponse{ID: inv.ID, Email: inv.Email, Role: inv.Role, Status: inv.Status, ExpiresAt: inv.ExpiresAt}
}

// ConfigureInvitations attaches state-changing team workflows to the existing
// team read handler. The limiter is mandatory because public credentials are
// otherwise a low-cost online guessing target.
func (h *HTTP) ConfigureInvitations(service *Service, limiter auth.LoginLimiter) error {
	if h == nil || service == nil || limiter == nil {
		return errors.New("team: invitation service and limiter are required")
	}
	h.invitations, h.invitationLimiter = service, limiter
	return nil
}

func (h *HTTP) invitationRoutes(mux *http.ServeMux, sessions *auth.HTTP) {
	if h.invitations == nil || h.invitationLimiter == nil {
		return
	}
	write := func(next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("team.write", next))).ServeHTTP(w, r)
		})
	}
	read := func(next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			sessions.RequireAuth(auth.RequirePermission("team.write", next)).ServeHTTP(w, r)
		})
	}
	mux.Handle("POST /api/v1/team/invitations", write(h.invite))
	mux.Handle("GET /api/v1/team/invitations", read(h.listInvitations))
	mux.Handle("POST /api/v1/team/invitations/{id}/resend", write(h.resend))
	mux.Handle("DELETE /api/v1/team/invitations/{id}", write(h.revoke))
	mux.Handle("PUT /api/v1/team/members/{id}/role", write(h.changeRole))
	mux.Handle("POST /api/v1/team/members/{id}/deactivate", write(h.deactivate))
	mux.HandleFunc("POST /api/v1/staff-invitations/prepare", h.publicNoStore(h.prepareInvitation(sessions)))
	mux.HandleFunc("POST /api/v1/staff-invitations/complete", h.publicNoStore(h.completeInvitation(sessions)))
}
func (h *HTTP) listInvitations(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	invitations, err := h.invitations.ListInvitations(r.Context(), principal)
	if err != nil {
		h.writeMutationError(w, r, err)
		return
	}
	response := struct {
		Data []invitationProjectionResponse `json:"data"`
	}{Data: make([]invitationProjectionResponse, 0, len(invitations))}
	for _, invitation := range invitations {
		response.Data = append(response.Data, invitationProjection(invitation))
	}
	writeJSON(w, http.StatusOK, response)
}
func (h *HTTP) publicNoStore(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Cache-Control", "no-store"); next(w, r) }
}

func (h *HTTP) invite(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	var input struct {
		Email    string      `json:"email"`
		Role     BuiltInRole `json:"role"`
		Password string      `json:"password"`
		MFACode  string      `json:"mfa_code"`
	}
	if !decodeInvitationJSON(w, r, &input) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	inv, err := h.invitations.Invite(r.Context(), InviteInput{Principal: principal, Email: input.Email, Role: input.Role, Password: input.Password, MFACode: input.MFACode})
	if err != nil {
		h.writeMutationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, invitationResponse{Invitation: invitationProjection(inv)})
}
func (h *HTTP) resend(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	var input struct {
		Password string `json:"password"`
		MFACode  string `json:"mfa_code"`
	}
	invitationID := r.PathValue("id")
	if !decodeInvitationJSON(w, r, &input) || !validUUID(invitationID) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	inv, err := h.invitations.ResendWithResult(r.Context(), ResendInput{Principal: principal, InvitationID: invitationID, Password: input.Password, MFACode: input.MFACode})
	if err != nil {
		h.writeMutationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, invitationResponse{Invitation: invitationProjection(inv)})
}
func (h *HTTP) revoke(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	var input struct {
		Password string `json:"password"`
		MFACode  string `json:"mfa_code"`
	}
	invitationID := r.PathValue("id")
	if !decodeInvitationJSON(w, r, &input) || !validUUID(invitationID) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	if err := h.invitations.Revoke(r.Context(), RevokeInput{Principal: principal, InvitationID: invitationID, Password: input.Password, MFACode: input.MFACode}); err != nil {
		h.writeMutationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *HTTP) changeRole(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	var input struct {
		Role     BuiltInRole `json:"role"`
		Password string      `json:"password"`
		MFACode  string      `json:"mfa_code"`
	}
	userID := r.PathValue("id")
	if !decodeInvitationJSON(w, r, &input) || !validUUID(userID) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	if err := h.invitations.ChangeRole(r.Context(), RoleChangeInput{Principal: principal, UserID: userID, Role: input.Role, Password: input.Password, MFACode: input.MFACode}); err != nil {
		h.writeMutationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *HTTP) deactivate(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	var input struct {
		Password string `json:"password"`
		MFACode  string `json:"mfa_code"`
	}
	userID := r.PathValue("id")
	if !decodeInvitationJSON(w, r, &input) || !validUUID(userID) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	if err := h.invitations.Deactivate(r.Context(), DeactivateInput{Principal: principal, UserID: userID, Password: input.Password, MFACode: input.MFACode}); err != nil {
		h.writeMutationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) prepareInvitation(sessions *auth.HTTP) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Token string `json:"token"`
		}
		if !decodeInvitationJSON(w, r, &input) || !h.limitInvitation(r, input.Token, sessions) {
			h.invalidInvitation(w, r)
			return
		}
		setup, err := h.invitations.PrepareAcceptance(r.Context(), input.Token)
		if err != nil {
			h.invalidInvitation(w, r)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			MFASetup MFASetup `json:"mfa_setup"`
		}{setup})
	}
}
func (h *HTTP) completeInvitation(sessions *auth.HTTP) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input CompleteInvitationInput
		if !decodeInvitationJSON(w, r, &input) || !h.limitInvitation(r, input.Token, sessions) {
			h.invalidInvitation(w, r)
			return
		}
		if err := h.invitations.CompleteAcceptance(r.Context(), input); err != nil {
			h.invalidInvitation(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func (h *HTTP) limitInvitation(r *http.Request, token string, sessions *auth.HTTP) bool {
	// Bound before hashing so malformed input cannot create unbounded Redis keys.
	if len(token) > 1024 {
		token = token[:1024]
	}
	digest, ok := invitationDigest(token)
	if !ok {
		digest = invitationDigestBytes(token)
	}
	key := "team:invite:token:" + hex.EncodeToString(digest)
	allowed, err := h.invitationLimiter.AllowSlidingWindow(r.Context(), key, 5, time.Minute)
	if err != nil || !allowed {
		return false
	}
	ip := sessions.ClientIP(r)
	sum := sha256.Sum256([]byte(ip))
	allowed, err = h.invitationLimiter.AllowSlidingWindow(r.Context(), "team:invite:ip:"+hex.EncodeToString(sum[:]), 20, time.Minute)
	return ok && err == nil && allowed
}
func (h *HTTP) invalidInvitation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	security.WriteError(w, r, http.StatusBadRequest, "INVALID_INVITATION", "Invitation is invalid or no longer available.")
}
func (h *HTTP) writeMutationError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if errors.Is(err, ErrStepUpFailed) {
		security.WriteError(w, r, http.StatusUnauthorized, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
		return
	}
	if errors.Is(err, ErrInvitationInvalid) || errors.Is(err, ErrStaffConflict) || errors.Is(err, ErrLastAdministrator) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request cannot be completed.")
		return
	}
	security.WriteError(w, r, http.StatusServiceUnavailable, "TEAM_UNAVAILABLE", "Team data is temporarily unavailable.")
}
func decodeInvitationJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxInvitationBodyBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return false
	}
	return errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}

var _ = strings.TrimSpace
