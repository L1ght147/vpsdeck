package hub

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/L1ght147/vpsdeck/internal/report"
)

const maxReportBytes = 1 << 20

func (h *Hub) handleReport(w http.ResponseWriter, r *http.Request) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	srv, err := h.store.ServerByToken(tok)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "unknown token", http.StatusUnauthorized)
		return
	}
	if err != nil {
		slog.Error("lookup token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var rep report.Report
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReportBytes))
	if err := dec.Decode(&rep); err != nil {
		http.Error(w, "bad report: "+err.Error(), http.StatusBadRequest)
		return
	}
	rep.Name = SanitizeName(rep.Name)

	if err := h.store.SaveReport(srv.ID, &rep, time.Now()); err != nil {
		slog.Error("save report", "err", err, "server", srv.ID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if h.alerts != nil {
		h.alerts.OnReport(srv, &rep)
	}
	w.WriteHeader(http.StatusNoContent)
}
