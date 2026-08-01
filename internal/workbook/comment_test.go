package workbook

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryCommentsAreIdempotentRevisionedAndMentionAware(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "comments", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	created, err := repository.CreateCommentThread(ctx, book.ID, "alice", CreateCommentThreadInput{
		IdempotencyKey: "thread-one",
		SheetID:        sheet.ID,
		Range:          "B2:C3",
		Content:        "검토 부탁드립니다 @Bob@example.com @bob@example.com @alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Range != "B2:C3" || created.Revision != 1 || len(created.Messages) != 1 || len(created.Messages[0].Mentions) != 2 {
		t.Fatalf("created thread = %#v", created)
	}
	duplicate, err := repository.CreateCommentThread(ctx, book.ID, "alice", CreateCommentThreadInput{IdempotencyKey: "thread-one", SheetID: sheet.ID, Range: "A1", Content: "ignored"})
	if err != nil || duplicate.ID != created.ID || duplicate.Messages[0].Content != created.Messages[0].Content {
		t.Fatalf("idempotent create = %#v, %v", duplicate, err)
	}
	notifications, err := repository.ListMentionNotifications(ctx, []string{"bob@example.com"}, true, 50)
	if err != nil || len(notifications) != 1 || notifications[0].Range != "B2:C3" || notifications[0].ActorID != "alice" {
		t.Fatalf("mention notifications = %#v, %v", notifications, err)
	}
	if self, err := repository.ListMentionNotifications(ctx, []string{"alice"}, false, 50); err != nil || len(self) != 0 {
		t.Fatalf("self mention notifications = %#v, %v", self, err)
	}
	read, err := repository.MarkMentionNotificationRead(ctx, notifications[0].ID, []string{"Bob@example.com"})
	if err != nil || read.ReadAt == nil {
		t.Fatalf("read notification = %#v, %v", read, err)
	}

	message := created.Messages[0]
	updated, err := repository.UpdateCommentMessage(ctx, message.ID, "alice", UpdateCommentMessageInput{Content: "담당자 변경 @dana", ExpectedRevision: message.Revision})
	if err != nil || updated.Revision != 2 || updated.Messages[0].Revision != 2 || len(updated.Messages[0].Mentions) != 1 {
		t.Fatalf("updated message = %#v, %v", updated, err)
	}
	if _, err := repository.UpdateCommentMessage(ctx, message.ID, "alice", UpdateCommentMessageInput{Content: "stale", ExpectedRevision: message.Revision}); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale message update error = %v", err)
	}
	if old, err := repository.ListMentionNotifications(ctx, []string{"bob@example.com"}, false, 50); err != nil || len(old) != 0 {
		t.Fatalf("old notification survived edit = %#v, %v", old, err)
	}
	if current, err := repository.ListMentionNotifications(ctx, []string{"dana"}, true, 50); err != nil || len(current) != 1 {
		t.Fatalf("new notification = %#v, %v", current, err)
	}

	replied, err := repository.AddCommentReply(ctx, created.ID, "bob", CreateCommentReplyInput{IdempotencyKey: "reply-one", Content: "확인했습니다 @charlie"})
	if err != nil || replied.Revision != 3 || len(replied.Messages) != 2 {
		t.Fatalf("reply = %#v, %v", replied, err)
	}
	replyDuplicate, err := repository.AddCommentReply(ctx, created.ID, "bob", CreateCommentReplyInput{IdempotencyKey: "reply-one", Content: "ignored"})
	if err != nil || len(replyDuplicate.Messages) != 2 {
		t.Fatalf("reply idempotency = %#v, %v", replyDuplicate, err)
	}
	resolvedValue := true
	resolved, err := repository.UpdateCommentThread(ctx, created.ID, "bob", UpdateCommentThreadInput{Resolved: &resolvedValue, ExpectedRevision: replied.Revision})
	if err != nil || !resolved.Resolved || resolved.ResolvedBy != "bob" || resolved.ResolvedAt == nil || resolved.Revision != 4 {
		t.Fatalf("resolved thread = %#v, %v", resolved, err)
	}
	reopenedValue := false
	if _, err := repository.UpdateCommentThread(ctx, created.ID, "bob", UpdateCommentThreadInput{Resolved: &reopenedValue, ExpectedRevision: replied.Revision}); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale thread update error = %v", err)
	}
	reopened, err := repository.AddCommentReply(ctx, created.ID, "alice", CreateCommentReplyInput{IdempotencyKey: "reply-reopen", Content: "추가 확인 사항"})
	if err != nil || reopened.Resolved || reopened.ResolvedAt != nil || reopened.ResolvedBy != "" || reopened.Revision != 5 || len(reopened.Messages) != 3 {
		t.Fatalf("reopened thread = %#v, %v", reopened, err)
	}
	deleted, err := repository.DeleteCommentMessage(ctx, replied.Messages[1].ID, "bob", replied.Messages[1].Revision)
	if err != nil || deleted.Messages[1].DeletedAt == nil || deleted.Messages[1].Content != "" || deleted.Messages[1].Revision != 2 {
		t.Fatalf("deleted reply = %#v, %v", deleted, err)
	}
	if err := repository.DeleteCommentThread(ctx, created.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner deleted thread: %v", err)
	}
	if err := repository.DeleteCommentThread(ctx, created.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetCommentThread(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted thread error = %v", err)
	}
}

func TestMemoryCommentAnchorsAndNotificationsFollowStructure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "comment anchors", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	thread, err := repository.CreateCommentThread(ctx, book.ID, "alice", CreateCommentThreadInput{IdempotencyKey: "anchor", SheetID: sheet.ID, Range: "B2:C3", Content: "@bob 확인"})
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "anchor-insert", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := repository.GetCommentThread(ctx, thread.ID)
	if err != nil || moved.Range != "B3:C4" || moved.Revision != 2 {
		t.Fatalf("moved thread = %#v, %v", moved, err)
	}
	notifications, err := repository.ListMentionNotifications(ctx, []string{"bob"}, false, 50)
	if err != nil || len(notifications) != 1 || notifications[0].Range != "B3:C4" {
		t.Fatalf("moved notification = %#v, %v", notifications, err)
	}
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: inserted.ServerVersion, IdempotencyKey: "anchor-delete", Axis: "row", Action: "delete", Index: 3, Count: 2}); err != nil {
		t.Fatal(err)
	}
	removed, err := repository.GetCommentThread(ctx, thread.ID)
	if err != nil || removed.Range != "#REF!" || removed.Revision != 3 {
		t.Fatalf("deleted anchor = %#v, %v", removed, err)
	}
	notifications, err = repository.ListMentionNotifications(ctx, []string{"bob"}, false, 50)
	if err != nil || len(notifications) != 1 || notifications[0].Range != "#REF!" {
		t.Fatalf("deleted notification anchor = %#v, %v", notifications, err)
	}
}
