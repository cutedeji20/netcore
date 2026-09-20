package devices

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

type HTTP struct{ service *Service }

func NewHTTP(service *Service) (*HTTP, error) {
	if service == nil {
		return nil, errors.New("devices: service is required")
	}
	return &HTTP{service: service}, nil
}
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("devices: mux and session authentication are required")
	}
	mux.Handle("GET /api/v1/portal/devices", sessions.RequireAuth(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/portal/devices", sessions.RequireAuth(sessions.RequireAllowedOrigin(http.HandlerFunc(h.register))))
	return nil
}
func (h *HTTP) principal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || !validUUID(p.TenantID) || !validUUID(p.UserID) {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return auth.Principal{}, false
	}
	return p, true
}
func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	values, err := h.service.List(r.Context(), p.TenantID, p.UserID)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "DEVICES_UNAVAILABLE", "Your devices are temporarily unavailable. Please try again shortly.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data []Device `json:"data"`
	}{Data: values})
}
func (h *HTTP) register(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	var input struct {
		MAC   string `json:"mac"`
		Label string `json:"label"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_DEVICE", "Device details are invalid.")
		return
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_DEVICE", "Device details are invalid.")
		return
	}
	device, err := h.service.Register(r.Context(), p.TenantID, p.UserID, input.MAC, input.Label)
	switch {
	case errors.Is(err, ErrInvalidRegistration):
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_DEVICE", "Device details are invalid.")
	case errors.Is(err, ErrDuplicateMAC):
		security.WriteError(w, r, http.StatusConflict, "DEVICE_ALREADY_REGISTERED", "This device is already registered.")
	case err != nil:
		security.WriteError(w, r, http.StatusServiceUnavailable, "DEVICES_UNAVAILABLE", "Your devices are temporarily unavailable. Please try again shortly.")
	default:
		writeJSON(w, http.StatusCreated, struct {
			Data Device `json:"data"`
		}{Data: device})
	}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
