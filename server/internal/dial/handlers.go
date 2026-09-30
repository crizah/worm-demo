package dial

import (
	"encoding/json"
	"net/http"
)

type stateResponse struct {
	Value    int `json:"value"`
	Baseline int `json:"baseline"`
	Max      int `json:"max"`
}

// Handlers wires Service to HTTP. GET returns current state, POST sets a
// new desired value (clamped server-side, see Service.Set).
//
// TODO: rate limit POST per visitor (IP/session token) before this goes
// public - deliberately not done yet, per docs/demo-app-plan.md.
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	writeState(w, h.svc.Get())
}

type setRequest struct {
	Value int `json:"value"`
}

func (h *Handlers) Set(w http.ResponseWriter, r *http.Request) {
	var req setRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}

	writeState(w, h.svc.Set(req.Value))
}

func writeState(w http.ResponseWriter, value int) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stateResponse{
		Value:    value,
		Baseline: Baseline,
		Max:      Max,
	})
}
