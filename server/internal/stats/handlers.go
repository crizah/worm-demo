package stats

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Handlers struct {
	db *sql.DB
}

func NewHandlers(db *sql.DB) *Handlers {
	return &Handlers{db: db}
}

// Stream pushes a {totalWrites, writesPerSec} JSON payload over SSE once a
// second until the client disconnects. writesPerSec is the delta since the
// previous tick, not an average.
//
// NOTE: an open SSE connection holds one middleware.MaxInFlight slot for
// its whole lifetime (unlike a normal request, which releases it
// immediately) - fine at demo-sized visitor counts, but if concurrent
// viewers approach MaxInFlight, bump that config rather than this handler.
func (h *Handlers) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var prev int64 = -1
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap, err := Read(ctx, h.db)
			if err != nil {
				continue // transient - next tick tries again
			}
			var rate int64
			if prev >= 0 {
				rate = snap.TotalWrites - prev
			}
			prev = snap.TotalWrites

			payload, _ := json.Marshal(map[string]int64{
				"totalWrites":  snap.TotalWrites,
				"writesPerSec": rate,
			})
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}
