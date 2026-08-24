package mail

import (
	"fmt"
	"mime"
	"strings"
	"time"
)

// compose builds a MIME message. Korean subjects and bodies are encoded so
// relays and clients that predate UTF-8 headers still show them correctly.
func compose(config Config, message Message) string {
	var builder strings.Builder
	builder.WriteString("From: " + encodeAddress(config.Address()) + "\r\n")
	builder.WriteString("To: " + message.To + "\r\n")
	builder.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", message.Subject) + "\r\n")
	builder.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	builder.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	builder.WriteString("Auto-Submitted: auto-generated\r\n")
	builder.WriteString("X-Kanpic-Notification: 1\r\n")
	builder.WriteString("\r\n")
	builder.WriteString(normalizeBody(message.Body))
	return builder.String()
}

func encodeAddress(address string) string {
	open := strings.LastIndex(address, "<")
	if open <= 0 {
		return address
	}
	return mime.QEncoding.Encode("utf-8", strings.TrimSpace(address[:open])) + " " + address[open:]
}

// normalizeBody uses CRLF line endings and escapes a leading dot so a line of
// text can never terminate the DATA command early.
func normalizeBody(body string) string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if strings.HasPrefix(body, ".") {
		body = "." + body
	}
	body = strings.ReplaceAll(body, "\r\n.", "\r\n..")
	if !strings.HasSuffix(body, "\r\n") {
		body += "\r\n"
	}
	return body
}

// Notification is the content of one event mail before recipients are resolved.
type Notification struct {
	Event      string
	Subject    string
	Lines      []string
	WorkbookID string
	Link       string
}

// Render turns a notification into the message body, appending the workbook
// link and a footer that says why the mail arrived.
func (n Notification) Render(config Config) string {
	lines := append([]string{}, n.Lines...)
	if link := n.absoluteLink(config); link != "" {
		lines = append(lines, "", "바로 열기: "+link)
	}
	lines = append(lines, "", "—", "이 메일은 kanpic 알림 설정에 따라 자동으로 발송되었습니다.")
	return strings.Join(lines, "\n")
}

func (n Notification) absoluteLink(config Config) string {
	base := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if n.Link == "" {
		if base == "" || n.WorkbookID == "" {
			return ""
		}
		return fmt.Sprintf("%s/workbooks/%s", base, n.WorkbookID)
	}
	if strings.HasPrefix(n.Link, "http://") || strings.HasPrefix(n.Link, "https://") {
		return n.Link
	}
	if base == "" {
		return ""
	}
	return base + "/" + strings.TrimLeft(n.Link, "/")
}

// ShareGranted tells somebody a workbook is now shared with them.
func ShareGranted(actor, workbookTitle, workbookID, role string) Notification {
	return Notification{
		Event:      EventShareGranted,
		Subject:    fmt.Sprintf("[kanpic] %s 워크북이 공유되었습니다", workbookTitle),
		WorkbookID: workbookID,
		Lines: []string{
			fmt.Sprintf("%s 님이 '%s' 워크북을 공유했습니다.", actor, workbookTitle),
			fmt.Sprintf("권한: %s", role),
		},
	}
}

// CommentPosted tells the people involved that a thread moved.
func CommentPosted(actor, workbookTitle, workbookID, rangeAddress, body string, reply bool) Notification {
	action := "댓글을 남겼습니다"
	if reply {
		action = "댓글에 답글을 남겼습니다"
	}
	return Notification{
		Event:      EventComment,
		Subject:    fmt.Sprintf("[kanpic] %s %s에 새 댓글", workbookTitle, rangeAddress),
		WorkbookID: workbookID,
		Lines: []string{
			fmt.Sprintf("%s 님이 '%s'의 %s에 %s.", actor, workbookTitle, rangeAddress, action),
			"",
			quote(body),
		},
	}
}

// WatchChanged 는 지켜보던 범위가 바뀌었을 때 보낸다. 무엇이 몇 칸
// 바뀌었는지만 적는다 — 바뀐 값을 메일에 실으면 워크북을 볼 권한이 없는
// 곳으로 자료가 새어 나간다.
func WatchChanged(actor, workbookTitle, workbookID, label string, ranges []string, firstCell string, cells int) Notification {
	where := strings.Join(ranges, ", ")
	if strings.TrimSpace(where) == "" {
		where = "시트 전체"
	}
	if strings.TrimSpace(label) != "" {
		where = label + " (" + where + ")"
	}
	return Notification{
		Event:      EventWatchChanged,
		Subject:    fmt.Sprintf("[kanpic] %s의 %s이(가) 바뀌었습니다", workbookTitle, where),
		WorkbookID: workbookID,
		Lines: []string{
			fmt.Sprintf("%s 님이 '%s'에서 지켜보시는 %s을(를) 바꿨습니다.", actor, workbookTitle, where),
			fmt.Sprintf("%s부터 %d개 칸이 바뀌었습니다.", firstCell, cells),
			"",
			"바뀐 값은 이 메일에 담지 않습니다. 워크북을 열어 확인하세요.",
		},
	}
}

// Mentioned is sent to somebody named with @ in a comment.
func Mentioned(actor, workbookTitle, workbookID, rangeAddress, body string) Notification {
	return Notification{
		Event:      EventMention,
		Subject:    fmt.Sprintf("[kanpic] %s 님이 댓글에서 회원님을 언급했습니다", actor),
		WorkbookID: workbookID,
		Lines: []string{
			fmt.Sprintf("%s 님이 '%s'의 %s 댓글에서 회원님을 언급했습니다.", actor, workbookTitle, rangeAddress),
			"",
			quote(body),
		},
	}
}

// AccessRequested tells an owner that somebody is waiting for access.
func AccessRequested(actor, workbookTitle, workbookID, role, reason string) Notification {
	lines := []string{fmt.Sprintf("%s 님이 '%s' 워크북의 %s 권한을 요청했습니다.", actor, workbookTitle, role)}
	if strings.TrimSpace(reason) != "" {
		lines = append(lines, "", quote(reason))
	}
	return Notification{Event: EventAccessRequest, Subject: fmt.Sprintf("[kanpic] %s 워크북 액세스 요청", workbookTitle), WorkbookID: workbookID, Lines: lines}
}

// AccessDecided tells a requester what the owner decided.
func AccessDecided(workbookTitle, workbookID, decision, role string) Notification {
	result := "거절되었습니다"
	if decision == "approved" {
		result = fmt.Sprintf("승인되었습니다. 권한: %s", role)
	}
	return Notification{
		Event:      EventAccessDecided,
		Subject:    fmt.Sprintf("[kanpic] %s 워크북 액세스 요청이 처리되었습니다", workbookTitle),
		WorkbookID: workbookID,
		Lines:      []string{fmt.Sprintf("'%s' 워크북에 대한 액세스 요청이 %s", workbookTitle, result)},
	}
}

// TestMessage proves the relay works from the settings screen.
func TestMessage() Notification {
	return Notification{
		Event:   EventTest,
		Subject: "[kanpic] SMTP 발송 테스트",
		Lines:   []string{"kanpic 관리자 화면에서 보낸 테스트 메일입니다.", "이 메일을 받았다면 SMTP 설정이 정상입니다."},
	}
}

func quote(body string) string {
	trimmed := strings.TrimSpace(body)
	if len([]rune(trimmed)) > 500 {
		trimmed = string([]rune(trimmed)[:500]) + "…"
	}
	lines := strings.Split(trimmed, "\n")
	for index, line := range lines {
		lines[index] = "> " + line
	}
	return strings.Join(lines, "\n")
}
