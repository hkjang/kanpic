package httpapi

import (
	"context"
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

// listDeletedWorkbooks returns the trash for the caller. A deleted workbook has
// no evaluable sharing, so only its owner and administrators see it.
func (s *Server) listDeletedWorkbooks(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListDeletedWorkbooks(r.Context(), r.URL.Query().Get("workspace_id"), s.accessPrincipal(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// restoreWorkbook and purgeWorkbook are owner-only: the authorization pass
// cannot resolve a trashed workbook, so ownership is checked here.
func (s *Server) restoreWorkbook(w http.ResponseWriter, r *http.Request) {
	workbookID := r.PathValue("workbookId")
	if !s.ownsTrashedWorkbook(w, r, workbookID) {
		return
	}
	restored, err := s.repository.RestoreWorkbook(r.Context(), workbookID, actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, restored)
}

func (s *Server) purgeWorkbook(w http.ResponseWriter, r *http.Request) {
	workbookID := r.PathValue("workbookId")
	if !s.ownsTrashedWorkbook(w, r, workbookID) {
		return
	}
	if err := s.repository.PurgeWorkbook(r.Context(), workbookID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ownsTrashedWorkbook(w http.ResponseWriter, r *http.Request, workbookID string) bool {
	principal := s.accessPrincipal(r)
	items, err := s.repository.ListDeletedWorkbooks(r.Context(), "", principal)
	if err != nil {
		s.writeError(w, r, err)
		return false
	}
	for _, item := range items {
		if item.ID == workbookID {
			return true
		}
	}
	s.writeError(w, r, workbook.ErrNotFound)
	return false
}

// setWorkbookFavorite stores a personal star. Any collaborator who can read the
// workbook may star it, which is why this is a separate route from the workbook
// update that editors use.
func (s *Server) setWorkbookFavorite(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Favorite bool `json:"favorite"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	workbookID := r.PathValue("workbookId")
	if err := s.repository.SetWorkbookFavorite(r.Context(), workbookID, actorID(r), input.Favorite); err != nil {
		s.writeError(w, r, err)
		return
	}
	item, err := s.repository.GetWorkbook(r.Context(), workbookID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	item.Favorite = input.Favorite
	if access, ok := workbookAccessFrom(r); ok {
		item.AccessRole, item.AccessSource = access.Role, access.Source
	}
	writeJSON(w, http.StatusOK, item)
}

// assertTrashOwner mirrors the REST trash guard for MCP callers.
func (s *Server) assertTrashOwner(ctx context.Context, r *http.Request, workbookID string) error {
	items, err := s.repository.ListDeletedWorkbooks(ctx, "", s.accessPrincipal(r))
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == workbookID {
			return nil
		}
	}
	return workbook.ErrNotFound
}

func (s *Server) sheetStats(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.SheetStats(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// copySheet copies a sheet into another workbook. The source needs read access
// and a permitted copy, and the target needs write access.
func (s *Server) copySheet(w http.ResponseWriter, r *http.Request) {
	var input workbook.CopySheetInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	if access, ok := workbookAccessFrom(r); ok && !access.CanCopy {
		s.writeCopyDenied(w, r, access)
		return
	}
	if strings.TrimSpace(input.TargetWorkbookID) == "" {
		s.writeError(w, r, workbook.ErrInvalid)
		return
	}
	if _, allowed := s.authorizeWorkbookID(w, r, input.TargetWorkbookID, workbook.CapabilityWrite); !allowed {
		return
	}
	created, err := s.repository.CopySheetToWorkbook(r.Context(), r.PathValue("sheetId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), input.TargetWorkbookID, actorID(r), "")
	writeJSON(w, http.StatusCreated, created)
}
