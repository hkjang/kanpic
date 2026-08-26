package httpapi

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

// 사용자를 CSV 로 한꺼번에 등록한다. 팀 하나를 들이려고 스무 번을 누르는
// 일을 없앤다.
//
// 미리보기와 실제 등록이 같은 코드로 읽는다. 따로 읽으면 미리보기에서 본
// 것과 실제로 만들어지는 것이 달라진다 — 사람은 미리보기를 믿고 누른다.

// importedUser 는 CSV 한 줄을 읽은 결과다.
type importedUser struct {
	Line   int    `json:"line"`
	UserID string `json:"user_id"`
	Name   string `json:"display_name,omitempty"`
	Email  string `json:"email,omitempty"`
	Note   string `json:"note,omitempty"`
	// Action 은 이 줄이 무엇을 할지다 — create, update, skip.
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// 머리글 이름. 한글로 적어 오는 것이 보통이므로 함께 받는다.
var userImportColumns = map[string]string{
	"user_id": "user_id", "userid": "user_id", "id": "user_id", "사용자": "user_id", "사용자id": "user_id", "아이디": "user_id",
	"display_name": "display_name", "name": "display_name", "이름": "display_name", "표시이름": "display_name",
	"email": "email", "메일": "email", "이메일": "email",
	"note": "note", "메모": "note", "비고": "note",
}

func normalizeColumn(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(key)
	// 위 표는 밑줄을 지운 꼴과 아닌 꼴을 함께 담고 있으므로 둘 다 본다.
	if name, ok := userImportColumns[key]; ok {
		return name
	}
	return userImportColumns[strings.ToLower(strings.TrimSpace(value))]
}

// parseUserCSV 는 붙여 넣은 글자를 줄 목록으로 읽는다.
//
// 머리글 줄이 있어야 한다. 자리로 읽으면 열 차례가 다른 파일을 말없이
// 엉뚱하게 읽어, 이름 칸에 이메일이 들어간 사용자가 스무 명 생긴다.
func parseUserCSV(text string) ([]importedUser, error) {
	reader := csv.NewReader(strings.NewReader(strings.TrimSpace(text)))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV 를 읽지 못했습니다: %w", err)
	}
	if len(records) == 0 {
		return nil, errors.New("내용이 없습니다")
	}
	index := map[string]int{}
	for position, cell := range records[0] {
		if name := normalizeColumn(cell); name != "" {
			index[name] = position
		}
	}
	if _, ok := index["user_id"]; !ok {
		return nil, errors.New("머리글 줄에 user_id(또는 사용자 ID) 열이 있어야 합니다")
	}
	at := func(row []string, name string) string {
		position, ok := index[name]
		if !ok || position >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[position])
	}
	seen := map[string]int{}
	items := make([]importedUser, 0, len(records)-1)
	for offset, row := range records[1:] {
		item := importedUser{
			Line: offset + 2, UserID: at(row, "user_id"), Name: at(row, "display_name"),
			Email: at(row, "email"), Note: at(row, "note"),
		}
		switch {
		case item.UserID == "":
			item.Action, item.Reason = "skip", "사용자 ID가 비어 있습니다"
		case seen[strings.ToLower(item.UserID)] > 0:
			// 같은 파일 안에 같은 아이디가 두 번 나오면 뒤엣것으로 덮어쓰지
			// 않고 짚어 준다. 어느 줄이 맞는지는 사람이 정해야 한다.
			item.Action = "skip"
			item.Reason = fmt.Sprintf("%d번째 줄과 사용자 ID가 같습니다", seen[strings.ToLower(item.UserID)])
		default:
			seen[strings.ToLower(item.UserID)] = item.Line
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, errors.New("머리글 말고는 줄이 없습니다")
	}
	return items, nil
}

// importUsers 는 CSV 를 읽어 미리 보여 주거나 실제로 등록한다.
func (s *Server) importUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct {
		CSV     string `json:"csv"`
		Preview bool   `json:"preview"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	items, err := parseUserCSV(input.CSV)
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %s", workbook.ErrInvalid, err.Error()))
		return
	}
	// 이미 있는 사람인지 먼저 본다. "새로 만듭니다" 라고 해 놓고 기존
	// 사용자의 이름을 덮어쓰면 사람은 무슨 일이 났는지 모른다.
	for position := range items {
		if items[position].Action == "skip" {
			continue
		}
		if _, lookupErr := s.repository.GetUser(r.Context(), items[position].UserID); lookupErr == nil {
			items[position].Action, items[position].Reason = "update", "이미 있는 사용자입니다. 이름·이메일·메모만 바뀝니다"
			continue
		}
		items[position].Action = "create"
	}
	if input.Preview {
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "applied": false})
		return
	}
	created, updated := 0, 0
	for position := range items {
		item := &items[position]
		if item.Action == "skip" {
			continue
		}
		upsert := workbook.UpsertUserInput{
			UserID: item.UserID, DisplayName: item.Name, Email: item.Email, Note: item.Note, ActorID: actorID(r),
		}
		if _, upsertErr := s.repository.UpsertUser(r.Context(), upsert); upsertErr != nil {
			// 한 줄이 잘못됐다고 나머지를 버리지 않는다. 무엇이 들어갔고
			// 무엇이 안 들어갔는지 줄마다 돌려준다.
			item.Action, item.Reason = "skip", upsertErr.Error()
			continue
		}
		if item.Action == "update" {
			updated++
			continue
		}
		created++
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "applied": true, "created": created, "updated": updated})
}
