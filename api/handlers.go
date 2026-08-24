package api

import (
	"net/http"

	"cacaoferment/service"
)

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	var req service.LockRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ErrorResponse{Code: service.CodeInvalidRequest, Message: "invalid JSON body"}})
		return
	}
	res, err := s.svc.Lock(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) handleBoxing(w http.ResponseWriter, r *http.Request) {
	var req service.BoxingRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ErrorResponse{Code: service.CodeInvalidRequest, Message: "invalid JSON body"}})
		return
	}
	req.TaskID = r.PathValue("id")
	res, err := s.svc.ConfirmBoxing(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleStartEquipment(w http.ResponseWriter, r *http.Request) {
	var req service.StartEquipmentRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ErrorResponse{Code: service.CodeInvalidRequest, Message: "invalid JSON body"}})
		return
	}
	req.TaskID = r.PathValue("id")
	res, err := s.svc.StartEquipment(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleTurnReadings(w http.ResponseWriter, r *http.Request) {
	var req service.TurnReadingsRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ErrorResponse{Code: service.CodeInvalidRequest, Message: "invalid JSON body"}})
		return
	}
	req.TaskID = r.PathValue("id")
	res, err := s.svc.SubmitTurnReadings(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleBlindSample(w http.ResponseWriter, r *http.Request) {
	var req service.BlindSampleRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ErrorResponse{Code: service.CodeInvalidRequest, Message: "invalid JSON body"}})
		return
	}
	req.TaskID = r.PathValue("id")
	req.BlindCode = r.PathValue("code")
	res, err := s.svc.SubmitBlindSample(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleChemistry(w http.ResponseWriter, r *http.Request) {
	var req service.ChemistryRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ErrorResponse{Code: service.CodeInvalidRequest, Message: "invalid JSON body"}})
		return
	}
	req.TaskID = r.PathValue("id")
	res, err := s.svc.SubmitChemistry(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRejudgement(w http.ResponseWriter, r *http.Request) {
	var req service.RejudgementRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ErrorResponse{Code: service.CodeInvalidRequest, Message: "invalid JSON body"}})
		return
	}
	req.TaskID = r.PathValue("id")
	res, err := s.svc.CreateRejudgement(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	var req service.ReviewRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ErrorResponse{Code: service.CodeInvalidRequest, Message: "invalid JSON body"}})
		return
	}
	req.TaskID = r.PathValue("id")
	res, err := s.svc.SubmitReview(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	var req service.FinalizeRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ErrorResponse{Code: service.CodeInvalidRequest, Message: "invalid JSON body"}})
		return
	}
	req.TaskID = r.PathValue("id")
	res, err := s.svc.Finalize(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
