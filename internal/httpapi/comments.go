package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/workbook"
)

func (s *Server) listCommentThreads(w http.ResponseWriter, r *http.Request) {
	includeResolved := false
	if value := strings.TrimSpace(r.URL.Query().Get("include_resolved")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: include_resolved must be a boolean", workbook.ErrInvalid))
			return
		}
		includeResolved = parsed
	}
	items, err := s.repository.ListCommentThreads(r.Context(), r.PathValue("workbookId"), r.URL.Query().Get("sheet_id"), includeResolved)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createCommentThread(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateCommentThreadInput
	if !decodeJSON(w, r, &input) {
		return
	}
	thread, err := s.repository.CreateCommentThread(r.Context(), r.PathValue("workbookId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishComment(thread, actorID(r), "created")
	writeJSON(w, http.StatusCreated, thread)
}

func (s *Server) getCommentThread(w http.ResponseWriter, r *http.Request) {
	thread, err := s.repository.GetCommentThread(r.Context(), r.PathValue("commentId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) createCommentReply(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateCommentReplyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	thread, err := s.repository.AddCommentReply(r.Context(), r.PathValue("commentId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishComment(thread, actorID(r), "replied")
	writeJSON(w, http.StatusCreated, thread)
}

func (s *Server) updateCommentThread(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateCommentThreadInput
	if !decodeJSON(w, r, &input) {
		return
	}
	thread, err := s.repository.UpdateCommentThread(r.Context(), r.PathValue("commentId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishComment(thread, actorID(r), "status_changed")
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) deleteCommentThread(w http.ResponseWriter, r *http.Request) {
	thread, err := s.repository.GetCommentThread(r.Context(), r.PathValue("commentId"))
	if err == nil {
		err = s.repository.DeleteCommentThread(r.Context(), thread.ID, actorID(r))
	}
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishComment(thread.WorkbookID, actorID(r), map[string]any{"thread_id": thread.ID, "action": "deleted"})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateCommentMessage(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateCommentMessageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	thread, err := s.repository.UpdateCommentMessage(r.Context(), r.PathValue("messageId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishComment(thread, actorID(r), "message_updated")
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) deleteCommentMessage(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseInt(r.URL.Query().Get("expected_revision"), 10, 64)
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: expected_revision is required", workbook.ErrInvalid))
		return
	}
	thread, err := s.repository.DeleteCommentMessage(r.Context(), r.PathValue("messageId"), actorID(r), revision)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishComment(thread, actorID(r), "message_deleted")
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) listMentionNotifications(w http.ResponseWriter, r *http.Request) {
	unreadOnly := false
	if value := strings.TrimSpace(r.URL.Query().Get("unread_only")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: unread_only must be a boolean", workbook.ErrInvalid))
			return
		}
		unreadOnly = parsed
	}
	limit := 0
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: limit must be an integer", workbook.ErrInvalid))
			return
		}
		limit = parsed
	}
	items, err := s.repository.ListMentionNotifications(r.Context(), actorAliases(r), unreadOnly, limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) markMentionNotificationRead(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.MarkMentionNotificationRead(r.Context(), r.PathValue("notificationId"), actorAliases(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) publishComment(thread workbook.CommentThread, actor, action string) {
	s.collab.PublishComment(thread.WorkbookID, actor, map[string]any{"thread_id": thread.ID, "sheet_id": thread.SheetID, "range": thread.Range, "revision": thread.Revision, "action": action})
}

func actorAliases(r *http.Request) []string {
	aliases := []string{actorID(r)}
	if user, ok := sessionUser(r); ok {
		aliases = append(aliases, user.Email, user.DisplayName)
	}
	return aliases
}
