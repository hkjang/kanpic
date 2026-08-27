package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kanpic/internal/workbook"
)

// 관리자 콘솔에서 한 일은 기록으로 남아야 한다. 권한을 나눠 주고 무엇을
// 했는지 볼 수 없으면 위임이 아니라 방치다.
func TestAdminActionsLeaveARecord(t *testing.T) {
	t.Parallel()
	var written bytes.Buffer
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewJSONHandler(&written, nil))))
	defer server.Close()

	request[workbook.DirectoryUser](t, server, http.MethodPost, "/api/v1/admin/users",
		map[string]any{"user_id": "nara.kim", "display_name": "김나라"}, http.StatusCreated)
	request[workbook.DirectoryUser](t, server, http.MethodPatch, "/api/v1/admin/users/nara.kim",
		map[string]any{"status": workbook.UserStatusSuspended}, http.StatusOK)
	request[workbook.DirectoryUser](t, server, http.MethodPost, "/api/v1/admin/users/nara.kim/roles",
		map[string]any{"role": "kanpic-analyst"}, http.StatusOK)

	lines := strings.Split(strings.TrimSpace(written.String()), "\n")
	found := map[string]map[string]any{}
	for _, line := range lines {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		message, _ := record["msg"].(string)
		if strings.HasPrefix(message, adminActionPrefix) {
			found[strings.TrimPrefix(message, adminActionPrefix)] = record
		}
	}
	for _, action := range []string{"user.create", "user.status", "user.role.grant"} {
		if _, ok := found[action]; !ok {
			t.Errorf("%s 기록이 없다. 남은 것: %v", action, keysOf(found))
		}
	}
	// 누가 무엇에 했는지가 들어 있어야 쓸모가 있다.
	status := found["user.status"]
	if status["target"] != "nara.kim" || status["status"] != workbook.UserStatusSuspended {
		t.Errorf("정지 기록 = %#v", status)
	}
	// 전역 관리자가 한 일인지 위임받은 사람이 한 일인지 갈라 적는다.
	if status["scope"] != "global" {
		t.Errorf("scope = %#v, 전역 관리자가 한 일이다", status["scope"])
	}
	if strings.TrimSpace(toText(status["actor"])) == "" {
		t.Errorf("누가 했는지가 비어 있다: %#v", status)
	}
}

// 위임받은 부서 관리자가 한 일은 그렇게 적혀야 한다. 감사에서 먼저 묻는
// 것이 "이 사람이 그럴 권한이 있었는가" 이다.
func TestDelegatedActionsAreMarkedAsDelegated(t *testing.T) {
	t.Parallel()
	var written bytes.Buffer
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewJSONHandler(&written, nil))))
	defer server.Close()
	manager, member, _ := seedDepartmentAdmin(t, server)
	written.Reset()

	requestAs[workbook.DirectoryUser](t, server, manager, http.MethodPatch, "/api/v1/admin/users/"+member,
		map[string]any{"status": workbook.UserStatusSuspended}, http.StatusOK)

	for _, line := range strings.Split(strings.TrimSpace(written.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		if record["msg"] != adminActionPrefix+"user.status" {
			continue
		}
		if record["scope"] != "department" || record["actor"] != manager {
			t.Fatalf("위임 기록 = %#v", record)
		}
		return
	}
	t.Fatalf("위임받은 사람이 한 정지가 기록되지 않았다: %s", written.String())
}

func keysOf(items map[string]map[string]any) []string {
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	return names
}

func toText(value any) string {
	text, _ := value.(string)
	return text
}
