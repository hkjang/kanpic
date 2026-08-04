package httpapi

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"

	"kanpic/internal/auth"
	"kanpic/internal/database"
	"kanpic/internal/settings"
	"kanpic/internal/workbook"
)

// The profile menu shows the administrator console from the session response,
// so a role granted in the console has to be visible there and not only to the
// request authorization. The check needs the settings the auth service reads,
// which is why it runs against a real PostgreSQL when one is configured.
func TestSessionAdminIncludesConsoleRoles(t *testing.T) {
	dsn := os.Getenv("KANPIC_TEST_DSN")
	if dsn == "" {
		t.Skip("KANPIC_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer pool.Close()
	settingRepository := settings.New(pool)
	if err := settingRepository.EnsureDefaults(ctx); err != nil {
		t.Fatalf("default settings: %v", err)
	}
	repository := workbook.NewMemoryRepository()
	server := &Server{repository: repository, settings: settingRepository, auth: auth.New(pool, settingRepository, auth.BootstrapCredentials{})}

	promoted, plain := "console.admin@corp.example", "plain.user@corp.example"
	for _, id := range []string{promoted, plain} {
		if err := repository.EnsureUser(ctx, id, id, id); err != nil {
			t.Fatalf("ensure user: %v", err)
		}
	}
	if _, err := repository.GrantUserRole(ctx, promoted, "kanpic-admin", "owner"); err != nil {
		t.Fatalf("grant role: %v", err)
	}

	request := httptest.NewRequest("GET", "/api/v1/session", nil)
	if !server.sessionIsAdmin(request, auth.User{ID: promoted}) {
		t.Error("a console granted administrator should be reported as an administrator")
	}
	if server.sessionIsAdmin(request, auth.User{ID: plain}) {
		t.Error("a user without an administrator role should not be reported as one")
	}
}
