// Package api exposes the JSON HTTP interface for the joint-inspection
// console. Handlers are thin: they decode a request, delegate to the service,
// and serialize the result. Stable error codes and deterministic reason
// ordering are preserved from the service layer.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"cacaoferment/service"
	"cacaoferment/store"
)

// ErrorResponse is the stable rejection payload.
type ErrorResponse struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Reasons []string `json:"reasons,omitempty"`
}

// Server wires the service and the embedded frontend together.
type Server struct {
	svc    *service.Service
	static http.Handler
}

// New constructs a Server. static may be nil to disable frontend serving.
func New(svc *service.Service, static http.Handler) *Server {
	return &Server{svc: svc, static: static}
}

// Handler returns the fully wired HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("GET /api/tasks/{id}/audit", s.handleAudit)
	mux.HandleFunc("POST /api/tasks/lock", s.handleLock)
	mux.HandleFunc("POST /api/tasks/{id}/boxing-confirmations", s.handleBoxing)
	mux.HandleFunc("POST /api/tasks/{id}/start-equipment", s.handleStartEquipment)
	mux.HandleFunc("POST /api/tasks/{id}/turn-readings", s.handleTurnReadings)
	mux.HandleFunc("POST /api/tasks/{id}/blind-samples/{code}/scores", s.handleBlindSample)
	mux.HandleFunc("POST /api/tasks/{id}/chemistry-readings", s.handleChemistry)
	mux.HandleFunc("POST /api/tasks/{id}/rejudgements", s.handleRejudgement)
	mux.HandleFunc("POST /api/tasks/{id}/reviews", s.handleReview)
	mux.HandleFunc("POST /api/tasks/{id}/finalize", s.handleFinalize)
	if s.static != nil {
		mux.Handle("/", s.static)
	}
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.Audit(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// writeError maps a service CodedError or store.ErrNotFound to the appropriate
// HTTP status and payload.
func writeError(w http.ResponseWriter, err error) {
	var ce *service.CodedError
	if errors.As(err, &ce) {
		status := statusForCode(ce.Code)
		writeJSON(w, status, map[string]any{"error": ErrorResponse{Code: ce.Code, Message: ce.Message, Reasons: ce.Reasons}})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": ErrorResponse{Code: service.CodeNotFound, Message: "not found"}})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": ErrorResponse{Code: service.CodeInternal, Message: err.Error()}})
}

func statusForCode(code string) int {
	switch code {
	case service.CodeResourceOccupied, service.CodeGenerationMismatch, service.CodeInvalidState,
		service.CodeAlreadyTerminal, service.CodeConflict, service.CodeBoxerOverlap,
		service.CodeReviewsIncomplete, service.CodeRevealLocked, service.CodeRejudgeExists,
		service.CodeInvalidDecision:
		return http.StatusConflict
	case service.CodeNotFound, service.CodeNotBlindFound:
		return http.StatusNotFound
	default:
		return http.StatusBadRequest
	}
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
