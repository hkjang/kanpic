package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/mail"
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
	s.notifyCommentMail(r, thread, false)
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
	s.notifyCommentMail(r, thread, true)
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

// notifyCommentMail tells the workbook audience about a new message and the
// mentioned people separately, so a mention is never buried in a thread mail.
func (s *Server) notifyCommentMail(r *http.Request, thread workbook.CommentThread, reply bool) {
	if s.mail == nil {
		return
	}
	message := thread.Messages[len(thread.Messages)-1]
	actor := actorID(r)
	label := s.actorLabel(r.Context(), actor)
	book, audience := s.workbookAudience(r.Context(), thread.WorkbookID)
	for _, participant := range thread.Messages {
		audience = append(audience, participant.AuthorID)
	}
	mentioned := message.Mentions
	audience = removeAll(audience, mentioned)
	s.notifyMail(r.Context(), mail.CommentPosted(label, book.Title, thread.WorkbookID, thread.Range, message.Content, reply), actor, audience)
	s.notifyMail(r.Context(), mail.Mentioned(label, book.Title, thread.WorkbookID, thread.Range, message.Content), actor, mentioned)
}

// removeAll drops the recipients that are handled by another mail.
func removeAll(values, excluded []string) []string {
	if len(excluded) == 0 {
		return values
	}
	skip := make(map[string]struct{}, len(excluded))
	for _, value := range excluded {
		skip[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if _, found := skip[strings.ToLower(strings.TrimSpace(value))]; found {
			continue
		}
		kept = append(kept, value)
	}
	return kept
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
