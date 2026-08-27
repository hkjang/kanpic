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

// revokeAnyKey 는 관리자가 남의 API 키를 끊는다.
//
// 지금까지 관리자는 전체 키를 보기만 했다. 키 하나가 새어 나가면 그 사람
// 계정을 통째로 정지하는 수밖에 없었는데, 그러면 키와 상관없는 그 사람의
// 일까지 멈춘다. 새어 나간 것만 끊을 수 있어야 한다.
//
// 저장소는 이미 관리자 회수를 받고 있었다. 문만 없었다.
func (s *Server) revokeAnyKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	keyID := r.PathValue("keyId")
	if err := s.keys.Revoke(r.Context(), keyID, actorID(r), true); err != nil {
		s.platformError(w, err)
		return
	}
	s.recordAdminAction(r, "apikey.revoke", keyID)
	w.WriteHeader(http.StatusNoContent)
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
