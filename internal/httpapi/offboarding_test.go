package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"kanpic/internal/workbook"
)

// 퇴사자를 정리하려면 그 사람이 무엇을 가지고 있는지 먼저 알아야 하고,
// 한 번에 넘길 수 있어야 한다. 워크북마다 하나씩 넘기는 것은 이미 있었는데,
// 마흔 개를 가진 사람이면 마흔 번을 눌러야 하고 몇 개를 빠뜨렸는지는
// 아무도 모른다.
func TestAdminHandsOverEverythingSomebodyOwns(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	for _, name := range []string{"자금 계획", "월말 마감", "지울 것"} {
		requestAs[workbook.Workbook](t, server, "gone.kim", http.MethodPost, "/api/v1/workbooks",
			map[string]any{"title": name}, http.StatusCreated)
	}
	request[workbook.DirectoryUser](t, server, http.MethodPost, "/api/v1/admin/users",
		map[string]any{"user_id": "next.park", "display_name": "박다음"}, http.StatusCreated)

	owned := request[map[string]any](t, server, http.MethodGet, "/api/v1/admin/users/gone.kim/workbooks", nil, http.StatusOK)
	if items, ok := owned["items"].([]any); !ok || len(items) != 3 {
		t.Fatalf("가진 워크북 = %#v", owned["items"])
	}

	result := request[map[string]any](t, server, http.MethodPost, "/api/v1/admin/users/gone.kim/workbooks:transfer",
		map[string]any{"new_owner_id": "next.park"}, http.StatusOK)
	transferred, _ := result["transferred"].([]any)
	failed, _ := result["failed"].([]any)
	if len(transferred) != 3 || len(failed) != 0 {
		t.Fatalf("넘긴 것 %d 개, 못 넘긴 것 %d 개: %#v", len(transferred), len(failed), result)
	}

	// 넘긴 뒤에는 예전 주인이 가진 것이 없어야 한다.
	after := request[map[string]any](t, server, http.MethodGet, "/api/v1/admin/users/gone.kim/workbooks", nil, http.StatusOK)
	if items, ok := after["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("아직 남아 있다: %#v", after["items"])
	}
	moved := request[map[string]any](t, server, http.MethodGet, "/api/v1/admin/users/next.park/workbooks", nil, http.StatusOK)
	if items, ok := moved["items"].([]any); !ok || len(items) != 3 {
		t.Fatalf("받은 것 = %#v", moved["items"])
	}
}

// 휴지통에 있는 것은 넘기지 않고 몇 개인지 센다. 조용히 빼면 "다 넘겼다" 는
// 말과 남아 있는 워크북이 어긋난다.
func TestAdminCountsTrashedWorkbooksInsteadOfHidingThem(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	kept := requestAs[workbook.Workbook](t, server, "gone.kim", http.MethodPost, "/api/v1/workbooks",
		map[string]any{"title": "남길 것"}, http.StatusCreated)
	dropped := requestAs[workbook.Workbook](t, server, "gone.kim", http.MethodPost, "/api/v1/workbooks",
		map[string]any{"title": "버릴 것"}, http.StatusCreated)
	requestAs[map[string]any](t, server, "gone.kim", http.MethodDelete, "/api/v1/workbooks/"+dropped.ID, nil, http.StatusNoContent)
	request[workbook.DirectoryUser](t, server, http.MethodPost, "/api/v1/admin/users",
		map[string]any{"user_id": "next.park", "display_name": "박다음"}, http.StatusCreated)

	owned := request[map[string]any](t, server, http.MethodGet, "/api/v1/admin/users/gone.kim/workbooks", nil, http.StatusOK)
	if trashed, _ := owned["trashed"].(float64); trashed != 1 {
		t.Fatalf("휴지통 수 = %#v", owned["trashed"])
	}
	result := request[map[string]any](t, server, http.MethodPost, "/api/v1/admin/users/gone.kim/workbooks:transfer",
		map[string]any{"new_owner_id": "next.park"}, http.StatusOK)
	if skipped, _ := result["skipped_trashed"].(float64); skipped != 1 {
		t.Fatalf("건너뛴 수 = %#v", result["skipped_trashed"])
	}
	if transferred, _ := result["transferred"].([]any); len(transferred) != 1 {
		t.Fatalf("넘긴 것 = %#v", result["transferred"])
	}
	_ = kept
}

// 정지된 사람에게 넘기면 받은 사람도 손댈 수 없다. 옮긴 것이 아니라 옮긴
// 척한 것이 되므로 미리 막는다.
func TestAdminRefusesToHandOverToASuspendedPerson(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	requestAs[workbook.Workbook](t, server, "gone.kim", http.MethodPost, "/api/v1/workbooks",
		map[string]any{"title": "자금 계획"}, http.StatusCreated)
	request[workbook.DirectoryUser](t, server, http.MethodPost, "/api/v1/admin/users",
		map[string]any{"user_id": "also.gone", "display_name": "또퇴사"}, http.StatusCreated)
	request[workbook.DirectoryUser](t, server, http.MethodPatch, "/api/v1/admin/users/also.gone",
		map[string]any{"status": workbook.UserStatusSuspended}, http.StatusOK)

	request[map[string]any](t, server, http.MethodPost, "/api/v1/admin/users/gone.kim/workbooks:transfer",
		map[string]any{"new_owner_id": "also.gone"}, http.StatusBadRequest)
	// 자기 자신에게 넘기는 것도 막는다.
	request[map[string]any](t, server, http.MethodPost, "/api/v1/admin/users/gone.kim/workbooks:transfer",
		map[string]any{"new_owner_id": "gone.kim"}, http.StatusBadRequest)
}

// 관리자만 쓸 수 있다.
func TestOwnedWorkbooksNeedsAnAdministrator(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	requestAs[map[string]any](t, server, "nobody", http.MethodGet, "/api/v1/admin/users/gone.kim/workbooks", nil, http.StatusForbidden)
}
