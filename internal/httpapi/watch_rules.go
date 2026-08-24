package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

// 지켜보기는 자기 것만 보고 자기 것만 만든다. 남이 무엇을 지켜보는지는
// 그 사람의 일이고, 남을 대신 등록하는 것은 시키지 않은 메일을 보내는 일이다.
func (s *Server) listWatchRules(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListWatchRules(r.Context(), r.PathValue("workbookId"), actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createWatchRule(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateWatchRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	// 누가 요청하든 자기 앞으로만 만든다.
	input.Watcher = actorID(r)
	item, err := s.repository.CreateWatchRule(r.Context(), r.PathValue("workbookId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateWatchRule(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateWatchRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !s.ownsWatchRule(w, r) {
		return
	}
	item, err := s.repository.UpdateWatchRule(r.Context(), r.PathValue("watchRuleId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteWatchRule(w http.ResponseWriter, r *http.Request) {
	if !s.ownsWatchRule(w, r) {
		return
	}
	expected, err := optionalRevision(r.URL.Query().Get("expected_revision"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeleteWatchRule(r.Context(), r.PathValue("watchRuleId"), actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ownsWatchRule 은 남의 지켜보기를 고치거나 지우지 못하게 막는다. 워크북을
// 볼 수 있다는 것과 남의 알림을 끌 수 있다는 것은 다른 이야기다.
func (s *Server) ownsWatchRule(w http.ResponseWriter, r *http.Request) bool {
	item, err := s.repository.GetWatchRule(r.Context(), r.PathValue("watchRuleId"))
	if err != nil {
		s.writeError(w, r, err)
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(item.Watcher), strings.TrimSpace(actorID(r))) {
		s.writeError(w, r, workbook.ErrForbidden)
		return false
	}
	return true
}
