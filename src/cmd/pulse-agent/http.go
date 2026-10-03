package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type blockRequest struct {
	IP     string `json:"ip"`
	TTL    string `json:"ttl"`
	Reason string `json:"reason,omitempty"`
}

type unblockRequest struct {
	IP string `json:"ip"`
}

func (a *agent) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", a.healthHandler)
	mux.HandleFunc("/block", a.blockHandler)
	mux.HandleFunc("/unblock", a.unblockHandler)
	mux.HandleFunc("/list", a.listHandler)
	return mux
}

func (a *agent) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	health := a.Health()
	status := http.StatusOK
	if health["status"] != "ok" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, health)
}

func (a *agent) blockHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var request blockRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	record, err := a.Block(request.IP, request.TTL, request.Reason)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errFirewallApply) {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, map[string]any{"error": err.Error(), "block": record})
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (a *agent) unblockHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var request unblockRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	record, found, err := a.Unblock(request.IP)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errFirewallApply) {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, map[string]any{"error": err.Error(), "block": record})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ip": record.IP, "removed": found, "status": record.Status, "result": record.Result})
}

func (a *agent) listHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	blocks := a.List()
	writeJSON(w, http.StatusOK, map[string]any{"blocks": blocks, "count": len(blocks)})
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
