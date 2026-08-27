package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"kanpic/internal/workbook"
)

// "링크 공개 47개" 를 보고 마흔일곱 번 누르는 사람은 없다. 몇 개는 남고,
// 남은 것은 아무도 세지 않는다.
func TestBulkRestrictClosesEveryOpenLink(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	open := make([]string, 0, 3)
	for _, title := range []string{"공개 1", "공개 2", "조직 공개"} {
		book := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks",
			map[string]any{"title": title}, http.StatusCreated)
		access := "anyone"
		if title == "조직 공개" {
			access = "organization"
		}
		request[map[string]any](t, server, http.MethodPatch, "/api/v1/workbooks/"+book.ID+"/sharing",
			map[string]any{"link_access": access}, http.StatusOK)
		open = append(open, book.ID)
	}

	result := request[map[string]any](t, server, http.MethodPost, "/api/v1/admin/workbooks:restrict-links",
		map[string]any{"filter": "anyone"}, http.StatusOK)
	if items, _ := result["restricted"].([]any); len(items) != 2 {
		t.Fatalf("닫은 수 = %#v", result["restricted"])
	}
	if remaining, _ := result["remaining"].(float64); remaining != 0 {
		t.Errorf("남은 수 = %#v", result["remaining"])
	}
	if items, _ := result["failed"].([]any); len(items) != 0 {
		t.Errorf("못 닫은 것 = %#v", result["failed"])
	}

	// 조직 공개는 건드리지 않는다. 고른 거르개만 닫는다.
	sharing := request[map[string]any](t, server, http.MethodGet, "/api/v1/workbooks/"+open[2]+"/sharing", nil, http.StatusOK)
	if inner, _ := sharing["sharing"].(map[string]any); inner["link_access"] != "organization" {
		t.Errorf("조직 공개가 바뀌었다: %#v", sharing["sharing"])
	}
	// 링크 공개였던 것은 닫혀 있어야 한다.
	closed := request[map[string]any](t, server, http.MethodGet, "/api/v1/workbooks/"+open[0]+"/sharing", nil, http.StatusOK)
	if inner, _ := closed["sharing"].(map[string]any); inner["link_access"] != "restricted" {
		t.Errorf("닫히지 않았다: %#v", closed["sharing"])
	}
}

// 닫을 수 있는 것은 링크로 열린 것뿐이다. 다른 거르개를 받으면 무엇을
// 닫는지 알 수 없는 채로 자료가 바뀐다.
func TestBulkRestrictRefusesFiltersItCannotClose(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	for _, filter := range []string{"all", "orphan", "trashed", "dormant", ""} {
		request[map[string]any](t, server, http.MethodPost, "/api/v1/admin/workbooks:restrict-links",
			map[string]any{"filter": filter}, http.StatusBadRequest)
	}
	requestAs[map[string]any](t, server, "nobody", http.MethodPost, "/api/v1/admin/workbooks:restrict-links",
		map[string]any{"filter": "anyone"}, http.StatusForbidden)
}
