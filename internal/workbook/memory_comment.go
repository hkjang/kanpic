package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/pkg/identity"
)

func (r *MemoryRepository) CreateCommentThread(_ context.Context, workbookID, actorID string, input CreateCommentThreadInput) (CommentThread, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		return CommentThread{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	rangeValue, err := normalizeCommentRange(input.Range)
	if err != nil {
		return CommentThread{}, err
	}
	content, mentions, err := normalizeCommentContent(input.Content)
	if err != nil {
		return CommentThread{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, exists := r.workbooks[workbookID]
	if !exists {
		return CommentThread{}, ErrNotFound
	}
	sheet, exists := state.sheets[input.SheetID]
	if !exists {
		return CommentThread{}, ErrNotFound
	}
	for _, thread := range r.comments {
		if thread.WorkbookID == workbookID && thread.CreatedBy == actorID && thread.IdempotencyKey == input.IdempotencyKey {
			return cloneCommentThread(thread), nil
		}
	}
	threadCount := 0
	for _, thread := range r.comments {
		if thread.WorkbookID == workbookID {
			threadCount++
		}
	}
	if threadCount >= MaxCommentThreads {
		return CommentThread{}, fmt.Errorf("%w: a workbook may contain up to %d comment threads", ErrInvalid, MaxCommentThreads)
	}
	now := r.now()
	thread := CommentThread{ID: identity.New(), WorkbookID: workbookID, SheetID: sheet.ID, SheetName: sheet.Name, Range: rangeValue, IdempotencyKey: input.IdempotencyKey, Revision: 1, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now}
	message := CommentMessage{ID: identity.New(), ThreadID: thread.ID, AuthorID: actorID, IdempotencyKey: input.IdempotencyKey, Content: content, Mentions: mentions, Revision: 1, CreatedAt: now, UpdatedAt: now}
	thread.Messages = []CommentMessage{message}
	r.comments[thread.ID] = cloneCommentThread(thread)
	r.replaceMemoryNotifications(thread, message)
	return cloneCommentThread(thread), nil
}

func (r *MemoryRepository) ListCommentThreads(_ context.Context, workbookID, sheetID string, includeResolved bool) ([]CommentThread, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, exists := r.workbooks[workbookID]
	if !exists {
		return nil, ErrNotFound
	}
	if sheetID != "" {
		if _, exists := state.sheets[sheetID]; !exists {
			return nil, ErrNotFound
		}
	}
	items := make([]CommentThread, 0)
	for _, thread := range r.comments {
		if thread.WorkbookID == workbookID && (sheetID == "" || thread.SheetID == sheetID) && (includeResolved || !thread.Resolved) {
			copy := cloneCommentThread(thread)
			if sheet, ok := state.sheets[thread.SheetID]; ok {
				copy.SheetName = sheet.Name
			}
			items = append(items, copy)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if len(items) > MaxCommentThreads {
		items = items[:MaxCommentThreads]
	}
	return items, nil
}

func (r *MemoryRepository) GetCommentThread(_ context.Context, id string) (CommentThread, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	thread, exists := r.comments[id]
	if !exists {
		return CommentThread{}, ErrNotFound
	}
	if state := r.workbooks[thread.WorkbookID]; state != nil {
		thread.SheetName = state.sheets[thread.SheetID].Name
	}
	return cloneCommentThread(thread), nil
}

func (r *MemoryRepository) AddCommentReply(_ context.Context, threadID, actorID string, input CreateCommentReplyInput) (CommentThread, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		return CommentThread{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	content, mentions, err := normalizeCommentContent(input.Content)
	if err != nil {
		return CommentThread{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	thread, exists := r.comments[threadID]
	if !exists {
		return CommentThread{}, ErrNotFound
	}
	for _, message := range thread.Messages {
		if message.AuthorID == actorID && message.IdempotencyKey == input.IdempotencyKey {
			return cloneCommentThread(thread), nil
		}
	}
	if len(thread.Messages) >= MaxCommentMessages {
		return CommentThread{}, fmt.Errorf("%w: a comment thread may contain up to %d messages", ErrInvalid, MaxCommentMessages)
	}
	now := r.now()
	message := CommentMessage{ID: identity.New(), ThreadID: threadID, AuthorID: actorID, IdempotencyKey: input.IdempotencyKey, Content: content, Mentions: mentions, Revision: 1, CreatedAt: now, UpdatedAt: now}
	thread.Messages = append(thread.Messages, message)
	thread.Resolved, thread.ResolvedBy, thread.ResolvedAt = false, "", nil
	thread.Revision++
	thread.UpdatedAt = now
	r.comments[threadID] = cloneCommentThread(thread)
	r.replaceMemoryNotifications(thread, message)
	return cloneCommentThread(thread), nil
}

func (r *MemoryRepository) UpdateCommentMessage(_ context.Context, messageID, actorID string, input UpdateCommentMessageInput) (CommentThread, error) {
	content, mentions, err := normalizeCommentContent(input.Content)
	if err != nil || input.ExpectedRevision < 1 {
		if err != nil {
			return CommentThread{}, err
		}
		return CommentThread{}, fmt.Errorf("%w: expected_revision is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	threadID, index, found := r.findMemoryCommentMessage(messageID)
	if !found {
		return CommentThread{}, ErrNotFound
	}
	thread := r.comments[threadID]
	message := thread.Messages[index]
	if message.AuthorID != actorID || message.DeletedAt != nil {
		return CommentThread{}, ErrNotFound
	}
	if message.Revision != input.ExpectedRevision {
		return CommentThread{}, ErrRevision
	}
	now := r.now()
	message.Content, message.Mentions, message.Revision, message.UpdatedAt = content, mentions, message.Revision+1, now
	thread.Messages[index], thread.Revision, thread.UpdatedAt = message, thread.Revision+1, now
	r.comments[threadID] = cloneCommentThread(thread)
	r.replaceMemoryNotifications(thread, message)
	return cloneCommentThread(thread), nil
}

func (r *MemoryRepository) DeleteCommentMessage(_ context.Context, messageID, actorID string, expectedRevision int64) (CommentThread, error) {
	if expectedRevision < 1 {
		return CommentThread{}, fmt.Errorf("%w: expected_revision is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	threadID, index, found := r.findMemoryCommentMessage(messageID)
	if !found {
		return CommentThread{}, ErrNotFound
	}
	thread := r.comments[threadID]
	message := thread.Messages[index]
	if message.AuthorID != actorID || message.DeletedAt != nil {
		return CommentThread{}, ErrNotFound
	}
	if message.Revision != expectedRevision {
		return CommentThread{}, ErrRevision
	}
	now := r.now()
	message.Content, message.Mentions, message.Revision, message.UpdatedAt, message.DeletedAt = "", []string{}, message.Revision+1, now, &now
	thread.Messages[index], thread.Revision, thread.UpdatedAt = message, thread.Revision+1, now
	r.comments[threadID] = cloneCommentThread(thread)
	r.replaceMemoryNotifications(thread, message)
	return cloneCommentThread(thread), nil
}

func (r *MemoryRepository) UpdateCommentThread(_ context.Context, threadID, actorID string, input UpdateCommentThreadInput) (CommentThread, error) {
	if input.ExpectedRevision < 1 || input.Resolved == nil {
		return CommentThread{}, fmt.Errorf("%w: resolved and expected_revision are required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	thread, exists := r.comments[threadID]
	if !exists {
		return CommentThread{}, ErrNotFound
	}
	if thread.Revision != input.ExpectedRevision {
		return CommentThread{}, ErrRevision
	}
	now := r.now()
	thread.Resolved, thread.Revision, thread.UpdatedAt = *input.Resolved, thread.Revision+1, now
	if *input.Resolved {
		thread.ResolvedBy, thread.ResolvedAt = actorID, &now
	} else {
		thread.ResolvedBy, thread.ResolvedAt = "", nil
	}
	r.comments[threadID] = cloneCommentThread(thread)
	return cloneCommentThread(thread), nil
}

func (r *MemoryRepository) DeleteCommentThread(_ context.Context, threadID, actorID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	thread, exists := r.comments[threadID]
	if !exists || thread.CreatedBy != actorID {
		return ErrNotFound
	}
	delete(r.comments, threadID)
	for id, notification := range r.notifications {
		if notification.ThreadID == threadID {
			delete(r.notifications, id)
		}
	}
	return nil
}

func (r *MemoryRepository) ListMentionNotifications(_ context.Context, aliases []string, unreadOnly bool, limit int) ([]MentionNotification, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("%w: notification limit must be between 1 and 200", ErrInvalid)
	}
	allowed := aliasesSet(aliases)
	if len(allowed) == 0 {
		return []MentionNotification{}, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]MentionNotification, 0)
	for _, notification := range r.notifications {
		if _, ok := allowed[strings.ToLower(notification.Recipient)]; ok && (!unreadOnly || notification.ReadAt == nil) {
			copy := cloneMentionNotification(notification)
			if state := r.workbooks[notification.WorkbookID]; state != nil {
				copy.SheetName = state.sheets[notification.SheetID].Name
			}
			items = append(items, copy)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (r *MemoryRepository) MarkMentionNotificationRead(_ context.Context, id string, aliases []string) (MentionNotification, error) {
	allowed := aliasesSet(aliases)
	r.mu.Lock()
	defer r.mu.Unlock()
	notification, exists := r.notifications[id]
	if !exists {
		return MentionNotification{}, ErrNotFound
	}
	if _, ok := allowed[strings.ToLower(notification.Recipient)]; !ok {
		return MentionNotification{}, ErrNotFound
	}
	if notification.ReadAt == nil {
		now := r.now()
		notification.ReadAt = &now
		r.notifications[id] = notification
	}
	return cloneMentionNotification(notification), nil
}

func (r *MemoryRepository) findMemoryCommentMessage(messageID string) (string, int, bool) {
	for threadID, thread := range r.comments {
		for index, message := range thread.Messages {
			if message.ID == messageID {
				return threadID, index, true
			}
		}
	}
	return "", 0, false
}

func (r *MemoryRepository) replaceMemoryNotifications(thread CommentThread, message CommentMessage) {
	for id, notification := range r.notifications {
		if notification.MessageID == message.ID {
			delete(r.notifications, id)
		}
	}
	for _, recipient := range message.Mentions {
		if strings.EqualFold(recipient, message.AuthorID) || message.DeletedAt != nil {
			continue
		}
		notification := MentionNotification{ID: identity.New(), Recipient: recipient, ActorID: message.AuthorID, WorkbookID: thread.WorkbookID, SheetID: thread.SheetID, SheetName: thread.SheetName, ThreadID: thread.ID, MessageID: message.ID, Range: thread.Range, CreatedAt: message.UpdatedAt}
		r.notifications[notification.ID] = notification
	}
}
