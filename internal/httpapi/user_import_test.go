package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"kanpic/internal/workbook"
)

// 머리글 줄이 있어야 한다. 자리로 읽으면 열 차례가 다른 파일을 말없이
// 엉뚱하게 읽어, 이름 칸에 이메일이 들어간 사용자가 스무 명 생긴다.
func TestUserCSVNeedsAHeaderRow(t *testing.T) {
	t.Parallel()
	if _, err := parseUserCSV("kim.nara,김나라,kim@corp.example"); err == nil {
		t.Error("머리글 없는 파일을 받아들였다")
	}
	if _, err := parseUserCSV(""); err == nil {
		t.Error("빈 내용을 받아들였다")
	}
	if _, err := parseUserCSV("user_id,display_name"); err == nil {
		t.Error("머리글만 있는 파일을 받아들였다")
	}
}

// 한글 머리글로 적어 오는 것이 보통이다.
func TestUserCSVReadsKoreanHeaders(t *testing.T) {
	t.Parallel()
	items, err := parseUserCSV("사용자 ID,이름,이메일\nkim.nara,김나라,kim@corp.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].UserID != "kim.nara" || items[0].Name != "김나라" || items[0].Email != "kim@corp.example" {
		t.Errorf("읽은 것 = %#v", items)
	}
}

// 무엇으로 적혔는지 밝히고 온 명단은 밝힌 대로 읽는다. 엑셀의 'CSV UTF-8'
// 저장이 앞에 두는 표시가 머리글에 붙은 채로 오면, 첫 열이 user_id 로 보이지
// 않아 명단 전체가 되돌아간다.
func TestUserCSVReadsARosterThatSaysWhatItIs(t *testing.T) {
	t.Parallel()
	items, err := parseUserCSV("\ufeffuser_id,display_name\nkim.nara,김나라")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].UserID != "kim.nara" {
		t.Errorf("읽은 것 = %#v", items)
	}
}

// 명단은 쉼표로만 오지 않는다. 엑셀에서 칸을 골라 붙여 넣으면 탭으로,
// 소수점을 쉼표로 적는 로케일의 엑셀이 저장하면 세미콜론으로 갈려 오는데,
// 둘 다 워크북 가져오기는 이미 읽는 꼴이다 — 같은 파일이 어느 문으로
// 들어오느냐에 따라 다르게 읽힐 이유가 없다. 메모 칸의 세미콜론은 쉼표
// 파일을 세미콜론 파일로 바꾸지 않는다.
func TestUserCSVReadsWhicheverSeparatorCutsTheRoster(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"세미콜론":     "user_id;display_name;email\nkim.nara;김나라;kim@corp.example",
		"탭":        "user_id\tdisplay_name\temail\nkim.nara\t김나라\tkim@corp.example",
		"메모의 세미콜론": "user_id,display_name,email,note\nkim.nara,김나라,kim@corp.example,\"영업;서울;신규\"",
	} {
		items, err := parseUserCSV(text)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(items) != 1 || items[0].UserID != "kim.nara" || items[0].Name != "김나라" || items[0].Email != "kim@corp.example" {
			t.Errorf("%s: 읽은 것 = %#v", name, items)
		}
	}
}

// 같은 파일 안에 같은 아이디가 두 번 나오면 뒤엣것으로 덮어쓰지 않고
// 짚어 준다. 어느 줄이 맞는지는 사람이 정해야 한다.
func TestUserCSVPointsAtDuplicateRowsInsteadOfOverwriting(t *testing.T) {
	t.Parallel()
	items, err := parseUserCSV("user_id,display_name\nkim.nara,김나라\nkim.nara,김나라(신)\n,이름만")
	if err != nil {
		t.Fatal(err)
	}
	if items[1].Action != "skip" || items[1].Reason == "" {
		t.Errorf("두 번째 줄 = %#v", items[1])
	}
	if items[2].Action != "skip" {
		t.Errorf("아이디 없는 줄 = %#v", items[2])
	}
}

// 미리보기는 아무것도 바꾸지 않고, 이미 있는 사람인지 알려 준다.
// "새로 만듭니다" 라고 해 놓고 기존 사용자의 이름을 덮어쓰면 사람은
// 무슨 일이 났는지 모른다.
func TestUserImportPreviewsBeforeItWrites(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	request[workbook.DirectoryUser](t, server, http.MethodPost, "/api/v1/admin/users",
		map[string]any{"user_id": "old.hand", "display_name": "옛사람"}, http.StatusCreated)

	body := "user_id,display_name,email\nnew.one,새사람,new@corp.example\nold.hand,옛사람(갱신),old@corp.example"
	preview := request[map[string]any](t, server, http.MethodPost, "/api/v1/admin/users:import",
		map[string]any{"csv": body, "preview": true}, http.StatusOK)
	if applied, _ := preview["applied"].(bool); applied {
		t.Fatal("미리보기가 실제로 등록했다")
	}
	rows, _ := preview["items"].([]any)
	first, _ := rows[0].(map[string]any)
	second, _ := rows[1].(map[string]any)
	if first["action"] != "create" || second["action"] != "update" {
		t.Fatalf("미리보기 = %#v %#v", first, second)
	}
	// 미리보기 뒤에도 새 사용자는 아직 없어야 한다.
	request[map[string]any](t, server, http.MethodGet, "/api/v1/admin/users/new.one", nil, http.StatusNotFound)

	applied := request[map[string]any](t, server, http.MethodPost, "/api/v1/admin/users:import",
		map[string]any{"csv": body}, http.StatusOK)
	if created, _ := applied["created"].(float64); created != 1 {
		t.Errorf("만든 수 = %#v", applied["created"])
	}
	if updated, _ := applied["updated"].(float64); updated != 1 {
		t.Errorf("갱신한 수 = %#v", applied["updated"])
	}
	made := request[workbook.DirectoryUser](t, server, http.MethodGet, "/api/v1/admin/users/new.one", nil, http.StatusOK)
	if made.DisplayName != "새사람" {
		t.Errorf("만들어진 사용자 = %#v", made)
	}
}

func TestUserImportNeedsAnAdministrator(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	requestAs[map[string]any](t, server, "nobody", http.MethodPost, "/api/v1/admin/users:import",
		map[string]any{"csv": "user_id\nkim"}, http.StatusForbidden)
}
