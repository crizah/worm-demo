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
// new desired value (requests/sec), rejected outright (not silently
// clamped) if it's out of range. Rate limiting itself is a middleware
// concern - see internal/middleware.
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
	if req.Value < 0 {
		http.Error(w, "value must not be negative", http.StatusBadRequest)
		return
	}
	if req.Value > Max {
		http.Error(w, "unfortunately our hardware doesn't support safely going above this limit", http.StatusNotFound)
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
