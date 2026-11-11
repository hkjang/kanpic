package httpapi

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"kanpic/internal/apikey"
)

// An API key or an OAuth token can be narrowed to a few scopes. That is how an
// administrator keeps an agent away from what other people shared with its
// user — and before this, it also kept the agent away from the user's own
// workbooks: a reporting key with workbook.read could not write one cell of a
// sheet its own user had made, and getting range.write onto a Keycloak client
// scope is a ticket to another team.
//
// On a workbook the caller's user owns, the scopes in mcp.owner_scopes are
// added for that one call. Everything else is unchanged: the user must really
// be the owner (an administrator's implied owner role does not count), every
// workbook the call names must be that same workbook, and the list can only
// hold scopes that act on one workbook's content — never admin, API keys,
// sharing or deleting.

type mcpOwnerGrantKey struct{}

// mcpOwnerGrant works out once, and only when a scope check would otherwise
// fail, which scopes ownership adds to this call.
type mcpOwnerGrant struct {
	once    sync.Once
	resolve func() apikey.Principal
	granted apikey.Principal
}

func (grant *mcpOwnerGrant) principal() apikey.Principal {
	grant.once.Do(func() { grant.granted = grant.resolve() })
	return grant.granted
}

// withMCPOwnerGrant attaches the lazy owner grant for one tool call. A caller
// signed in with the browser, or a token without scopes, is not narrowed and
// gets nothing attached.
func (s *Server) withMCPOwnerGrant(r *http.Request, args map[string]any) *http.Request {
	principal, ok := apiPrincipal(r)
	if !ok {
		return r
	}
	request := r
	grant := &mcpOwnerGrant{resolve: func() apikey.Principal {
		scopes := s.mcpOwnerScopes(request.Context())
		if len(scopes) == 0 || !s.callerOwnsMCPTarget(request, principal.UserID, args) {
			return apikey.Principal{}
		}
		return apikey.NewPrincipal(principal.UserID, scopes)
	}}
	return r.WithContext(context.WithValue(r.Context(), mcpOwnerGrantKey{}, grant))
}

// mcpOwnerGranted reports whether ownership adds this scope to the call.
func mcpOwnerGranted(r *http.Request, scope string) bool {
	grant, ok := r.Context().Value(mcpOwnerGrantKey{}).(*mcpOwnerGrant)
	if !ok {
		return false
	}
	granted := grant.principal()
	return granted.UserID != "" && granted.Allows(scope)
}

// callerOwnsMCPTarget is true when the call names at least one workbook
// resource and every one of them belongs to the same workbook, owned by the
// caller's user.
func (s *Server) callerOwnsMCPTarget(r *http.Request, userID string, args map[string]any) bool {
	if strings.TrimSpace(userID) == "" {
		return false
	}
	workbookID := ""
	for _, candidate := range mcpResourceArgs {
		value := strings.TrimSpace(stringArg(args, candidate.arg))
		if value == "" {
			continue
		}
		resolved, found, err := s.resolveTargetWorkbook(r.Context(), r, authorizationTarget{kind: candidate.kind, id: value})
		if err != nil || !found || resolved == "" {
			return false
		}
		if workbookID != "" && resolved != workbookID {
			return false
		}
		workbookID = resolved
	}
	// A copy names its destination separately; widening for the source must
	// not reach into somebody else's workbook.
	if target := strings.TrimSpace(stringArg(args, "target_workbook_id")); target != "" && target != workbookID {
		return false
	}
	if workbookID == "" {
		return false
	}
	access, err := s.repository.ResolveWorkbookAccess(r.Context(), workbookID, s.accessPrincipal(r))
	return err == nil && access.OwnerID != "" && access.OwnerID == userID
}

// mcpOwnerScopes reads mcp.owner_scopes. Without a settings store (tests and
// the embedded server) the built-in default applies; when the store cannot be
// read nothing is added, because widening is the one thing that must not
// happen by accident.
func (s *Server) mcpOwnerScopes(ctx context.Context) []string {
	configured := apikey.DefaultOwnerScopes
	if s.settings != nil {
		values, err := s.settings.Values(ctx)
		if err != nil {
			return nil
		}
		if raw, present := values["mcp.owner_scopes"]; present {
			list, _ := raw.([]any)
			configured = make([]string, 0, len(list))
			for _, item := range list {
				if scope, ok := item.(string); ok {
					configured = append(configured, scope)
				}
			}
		}
	}
	scopes := make([]string, 0, len(configured))
	for _, scope := range configured {
		if apikey.OwnerGrantable(scope) {
			scopes = append(scopes, strings.TrimSpace(scope))
		}
	}
	return scopes
}
