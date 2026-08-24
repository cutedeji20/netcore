package portal

import (
	"errors"
	"net/http"

	"github.com/netcore-isp/netcore/internal/security"
)

// CatalogueHTTP exposes the tenant-bound public catalogue. It deliberately
// accepts no tenant selector, customer credential, price, or access context.
type CatalogueHTTP struct{ service *CatalogueService }

func NewCatalogueHTTP(service *CatalogueService) (*CatalogueHTTP, error) {
	if service == nil {
		return nil, errors.New("portal: catalogue service is required")
	}
	return &CatalogueHTTP{service: service}, nil
}

func (h *CatalogueHTTP) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/portal/catalogue", h.list)
}

func (h *CatalogueHTTP) list(w http.ResponseWriter, r *http.Request) {
	plans, err := h.service.PublishedPlans(r.Context())
	if errors.Is(err, ErrCatalogueNotFound) {
		security.WriteError(w, r, http.StatusNotFound, "PORTAL_UNAVAILABLE", "The access portal is not available.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "PORTAL_UNAVAILABLE", "The access portal is temporarily unavailable.")
		return
	}
	response := catalogueResponse{Data: make([]publicPlanResponse, 0, len(plans))}
	for _, plan := range plans {
		response.Data = append(response.Data, publicPlanResponse{
			ID: plan.ID, Name: plan.Name, Description: plan.Description, PriceMinor: plan.PriceMinor,
			Currency: plan.Currency, DurationSeconds: plan.DurationSeconds, DownloadBPS: plan.DownloadBPS,
			UploadBPS: plan.UploadBPS, MaxDevices: plan.MaxDevices, MaxConcurrentSessions: plan.MaxConcurrentSessions,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

type catalogueResponse struct {
	Data []publicPlanResponse `json:"data"`
}

type publicPlanResponse struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Description           string `json:"description,omitempty"`
	PriceMinor            int64  `json:"price_minor"`
	Currency              string `json:"currency"`
	DurationSeconds       int64  `json:"duration_seconds"`
	DownloadBPS           int64  `json:"download_bps"`
	UploadBPS             int64  `json:"upload_bps"`
	MaxDevices            int    `json:"max_devices"`
	MaxConcurrentSessions int    `json:"max_concurrent_sessions"`
}
