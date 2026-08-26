package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"kanpic/internal/workbook"
)

// 부서 관리자를 세우고, 그 아래 부서까지 구성원을 맡게 한다.
func seedDepartmentAdmin(t *testing.T, server *httptest.Server) (manager, member, outsider string) {
	t.Helper()
	manager, member, outsider = "lead.kim", "team.park", "other.lee"
	for _, id := range []string{manager, member, outsider} {
		request[workbook.DirectoryUser](t, server, http.MethodPost, "/api/v1/admin/users",
			map[string]any{"user_id": id, "display_name": id}, http.StatusCreated)
	}
	parent := request[workbook.Department](t, server, http.MethodPost, "/api/v1/departments",
		map[string]any{"name": "영업본부"}, http.StatusCreated)
	child := request[workbook.Department](t, server, http.MethodPost, "/api/v1/departments",
		map[string]any{"name": "영업1팀", "parent_id": parent.ID}, http.StatusCreated)
	other := request[workbook.Department](t, server, http.MethodPost, "/api/v1/departments",
		map[string]any{"name": "관리본부"}, http.StatusCreated)
	// 구성원은 아래 부서에, 관리자는 위 부서에 둔다. 부서가 나뉘어도
	// 맡은 사람이 바뀌지 않아야 한다.
	request[workbook.Department](t, server, http.MethodPost, "/api/v1/departments/"+child.ID+"/members",
		map[string]any{"user_ids": []string{member}}, http.StatusOK)
	request[workbook.Department](t, server, http.MethodPost, "/api/v1/departments/"+other.ID+"/members",
		map[string]any{"user_ids": []string{outsider}}, http.StatusOK)
	request[workbook.Department](t, server, http.MethodPost, "/api/v1/departments/"+parent.ID+"/managers",
		map[string]any{"user_ids": []string{manager}}, http.StatusOK)
	return manager, member, outsider
}

// 맡은 부서와 그 아래 부서의 구성원 계정은 다룰 수 있다.
func TestDepartmentAdminManagesItsOwnMembers(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	manager, member, outsider := seedDepartmentAdmin(t, server)

	// 목록에는 맡은 구성원만 나온다. 조직 전체 명부가 보이면 위임한 범위
	// 밖의 사람까지 알게 된다.
	listed := requestAs[map[string]any](t, server, manager, http.MethodGet, "/api/v1/admin/users", nil, http.StatusOK)
	rows, _ := listed["items"].([]any)
	if len(rows) != 1 {
		t.Fatalf("보이는 사용자 = %d 명: %#v", len(rows), listed["items"])
	}
	if scoped, _ := listed["scoped"].(bool); !scoped {
		t.Error("범위가 좁혀졌다고 알려 주지 않는다")
	}

	requestAs[workbook.DirectoryUser](t, server, manager, http.MethodGet, "/api/v1/admin/users/"+member, nil, http.StatusOK)
	requestAs[workbook.DirectoryUser](t, server, manager, http.MethodPatch, "/api/v1/admin/users/"+member,
		map[string]any{"status": workbook.UserStatusSuspended}, http.StatusOK)
	requestAs[workbook.DirectoryUser](t, server, manager, http.MethodPost, "/api/v1/admin/users/"+member+"/roles",
		map[string]any{"role": "kanpic-analyst"}, http.StatusOK)
	requestAs[map[string]any](t, server, manager, http.MethodDelete, "/api/v1/admin/users/"+member+"/sessions", nil, http.StatusOK)

	// 다른 부서 사람은 다룰 수 없다.
	requestAs[map[string]any](t, server, manager, http.MethodGet, "/api/v1/admin/users/"+outsider, nil, http.StatusForbidden)
	requestAs[map[string]any](t, server, manager, http.MethodPatch, "/api/v1/admin/users/"+outsider,
		map[string]any{"status": workbook.UserStatusSuspended}, http.StatusForbidden)
}

// 자기 자신은 다룰 수 없다. 스스로 정지를 풀거나 역할을 얹을 수 있으면
// 위임이 아니라 승격이다.
func TestDepartmentAdminCannotWorkOnItself(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	manager, _, _ := seedDepartmentAdmin(t, server)
	// 관리자를 자기 부서 구성원으로도 넣어 둔다.
	departments := request[map[string]any](t, server, http.MethodGet, "/api/v1/departments", nil, http.StatusOK)
	items, _ := departments["items"].([]any)
	for _, entry := range items {
		row, _ := entry.(map[string]any)
		if row["name"] == "영업1팀" {
			request[workbook.Department](t, server, http.MethodPost, "/api/v1/departments/"+row["id"].(string)+"/members",
				map[string]any{"user_ids": []string{manager}}, http.StatusOK)
		}
	}
	requestAs[map[string]any](t, server, manager, http.MethodPost, "/api/v1/admin/users/"+manager+"/roles",
		map[string]any{"role": "kanpic-admin"}, http.StatusForbidden)
}

// 자료를 옮기는 일과 조직 전체를 보는 일은 열지 않는다.
func TestDepartmentAdminCannotReachOwnershipOrSettings(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	manager, member, _ := seedDepartmentAdmin(t, server)

	for _, closed := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodGet, "/api/v1/admin/users/" + member + "/workbooks", nil},
		{http.MethodPost, "/api/v1/admin/users/" + member + "/workbooks:transfer", map[string]any{"new_owner_id": manager}},
		{http.MethodGet, "/api/v1/admin/overview", nil},
		{http.MethodGet, "/api/v1/admin/workbooks", nil},
		{http.MethodPost, "/api/v1/admin/users", map[string]any{"user_id": "sneaky"}},
		{http.MethodPost, "/api/v1/admin/users:import", map[string]any{"csv": "user_id\nsneaky"}},
	} {
		requestAs[map[string]any](t, server, manager, closed.method, closed.path, closed.body, http.StatusForbidden)
	}
}

// 부서 관리자는 전역 관리자만 지정한다.
func TestOnlyAGlobalAdminAppointsDepartmentManagers(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	manager, _, outsider := seedDepartmentAdmin(t, server)
	departments := request[map[string]any](t, server, http.MethodGet, "/api/v1/departments", nil, http.StatusOK)
	items, _ := departments["items"].([]any)
	row, _ := items[0].(map[string]any)
	requestAs[map[string]any](t, server, manager, http.MethodPost, "/api/v1/departments/"+row["id"].(string)+"/managers",
		map[string]any{"user_ids": []string{outsider}}, http.StatusForbidden)
}

// 부서 관리자가 구성원에게 관리자 역할을 얹으면 전역 관리자를 하나 만들어
// 내는 셈이다. 그것은 위임이 아니라 승격이고, 자기 자신을 막아 둔 것도
// 이 한 걸음으로 우회된다. 회수도 맡은 범위 밖이다.
func TestDepartmentAdminCannotMintAGlobalAdministrator(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	manager, member, _ := seedDepartmentAdmin(t, server)

	requestAs[map[string]any](t, server, manager, http.MethodPost, "/api/v1/admin/users/"+member+"/roles",
		map[string]any{"role": "kanpic-admin"}, http.StatusForbidden)
	// 평범한 역할은 그대로 줄 수 있어야 한다.
	requestAs[workbook.DirectoryUser](t, server, manager, http.MethodPost, "/api/v1/admin/users/"+member+"/roles",
		map[string]any{"role": "kanpic-analyst"}, http.StatusOK)

	// 전역 관리자가 얹은 관리자 역할을 부서 관리자가 뗄 수도 없다.
	request[workbook.DirectoryUser](t, server, http.MethodPost, "/api/v1/admin/users/"+member+"/roles",
		map[string]any{"role": "kanpic-admin"}, http.StatusOK)
	requestAs[map[string]any](t, server, manager, http.MethodDelete,
		"/api/v1/admin/users/"+member+"/roles/kanpic-admin", nil, http.StatusForbidden)
}
