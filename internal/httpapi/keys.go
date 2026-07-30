package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/apikey"
)

func (s *Server) listMyKeys(w http.ResponseWriter, r *http.Request) {
	items, err := s.keys.List(r.Context(), actorID(r), false)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listAllKeys(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := s.keys.List(r.Context(), actorID(r), true)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var input apikey.CreateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.keys.Create(r.Context(), actorID(r), input)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateKey(w http.ResponseWriter, r *http.Request) {
	var input apikey.UpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.keys.Update(r.Context(), r.PathValue("keyId"), actorID(r), input, false)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	if err := s.keys.Revoke(r.Context(), r.PathValue("keyId"), actorID(r), false); err != nil {
		s.platformError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("keyAction")
	if !strings.HasSuffix(action, ":rotate") {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSuffix(action, ":rotate")
	item, err := s.keys.Rotate(r.Context(), id, actorID(r), false)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
