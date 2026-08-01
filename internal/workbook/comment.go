package workbook

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"kanpic/pkg/cellrange"
)

const (
	MaxCommentContentRunes = 10_000
	MaxCommentMentions     = 50
	MaxCommentThreads      = 500
	MaxCommentMessages     = 200
)

var mentionPattern = regexp.MustCompile(`@([\p{L}\p{N}][\p{L}\p{N}._%+\-]*(?:@[\p{L}\p{N}][\p{L}\p{N}.\-]*[\p{L}\p{N}])?)`)

type CommentMessage struct {
	ID             string     `json:"id"`
	ThreadID       string     `json:"thread_id"`
	AuthorID       string     `json:"author_id"`
	IdempotencyKey string     `json:"-"`
	Content        string     `json:"content"`
	Mentions       []string   `json:"mentions"`
	Revision       int64      `json:"revision"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type CommentThread struct {
	ID             string           `json:"id"`
	WorkbookID     string           `json:"workbook_id"`
	SheetID        string           `json:"sheet_id"`
	SheetName      string           `json:"sheet_name"`
	Range          string           `json:"range"`
	IdempotencyKey string           `json:"-"`
	Resolved       bool             `json:"resolved"`
	Revision       int64            `json:"revision"`
	CreatedBy      string           `json:"created_by"`
	ResolvedBy     string           `json:"resolved_by,omitempty"`
	ResolvedAt     *time.Time       `json:"resolved_at,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	Messages       []CommentMessage `json:"messages"`
}

type CreateCommentThreadInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	SheetID        string `json:"sheet_id"`
	Range          string `json:"range"`
	Content        string `json:"content"`
}

type CreateCommentReplyInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	Content        string `json:"content"`
}

type UpdateCommentMessageInput struct {
	Content          string `json:"content"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type UpdateCommentThreadInput struct {
	Resolved         *bool `json:"resolved"`
	ExpectedRevision int64 `json:"expected_revision"`
}

type MentionNotification struct {
	ID         string     `json:"id"`
	Recipient  string     `json:"recipient"`
	ActorID    string     `json:"actor_id"`
	WorkbookID string     `json:"workbook_id"`
	SheetID    string     `json:"sheet_id"`
	SheetName  string     `json:"sheet_name"`
	ThreadID   string     `json:"thread_id"`
	MessageID  string     `json:"message_id"`
	Range      string     `json:"range"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func normalizeCommentRange(value string) (string, error) {
	selected, err := cellrange.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("%w: invalid comment range", ErrInvalid)
	}
	return cellrange.Address(selected.Start.Row, selected.Start.Column) + ":" + cellrange.Address(selected.End.Row, selected.End.Column), nil
}

func normalizeCommentContent(value string) (string, []string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > MaxCommentContentRunes {
		return "", nil, fmt.Errorf("%w: comment content must contain 1 to %d characters", ErrInvalid, MaxCommentContentRunes)
	}
	mentions := extractMentions(value)
	if len(mentions) > MaxCommentMentions {
		return "", nil, fmt.Errorf("%w: a comment may mention up to %d recipients", ErrInvalid, MaxCommentMentions)
	}
	return value, mentions, nil
}

func extractMentions(value string) []string {
	seen := make(map[string]string)
	for _, match := range mentionPattern.FindAllStringSubmatch(value, -1) {
		mention := strings.TrimSpace(match[1])
		key := strings.ToLower(mention)
		if mention != "" {
			if _, exists := seen[key]; !exists {
				seen[key] = mention
			}
		}
	}
	items := make([]string, 0, len(seen))
	for _, mention := range seen {
		items = append(items, mention)
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i]) < strings.ToLower(items[j]) })
	return items
}

func cloneCommentMessage(message CommentMessage) CommentMessage {
	message.Mentions = append([]string(nil), message.Mentions...)
	if message.Mentions == nil {
		message.Mentions = []string{}
	}
	if message.DeletedAt != nil {
		value := *message.DeletedAt
		message.DeletedAt = &value
	}
	return message
}

func cloneCommentThread(thread CommentThread) CommentThread {
	messages := thread.Messages
	thread.Messages = make([]CommentMessage, len(messages))
	for index, message := range messages {
		thread.Messages[index] = cloneCommentMessage(message)
	}
	if thread.ResolvedAt != nil {
		value := *thread.ResolvedAt
		thread.ResolvedAt = &value
	}
	return thread
}

func cloneMentionNotification(notification MentionNotification) MentionNotification {
	if notification.ReadAt != nil {
		value := *notification.ReadAt
		notification.ReadAt = &value
	}
	return notification
}

func aliasesSet(aliases []string) map[string]struct{} {
	result := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		if normalized := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(alias, "@"))); normalized != "" {
			result[normalized] = struct{}{}
		}
	}
	return result
}
