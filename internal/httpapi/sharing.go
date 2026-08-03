package httpapi

import (
	"context"
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

// sharingResponse is what the share dialog needs in one round trip: the stored
// sharing state and the caller's own effective access.
type sharingResponse struct {
	Sharing workbook.WorkbookSharing `json:"sharing"`
	Access  workbook.WorkbookAccess  `json:"access"`
}

func (s *Server) sharingWithAccess(r *http.Request, workbookID string) (sharingResponse, error) {
	sharing, err := s.repository.GetWorkbookSharing(r.Context(), workbookID)
	if err != nil {
		return sharingResponse{}, err
	}
	access, err := s.repository.ResolveWorkbookAccess(r.Context(), workbookID, s.accessPrincipal(r))
	if err != nil {
		return sharingResponse{}, err
	}
	return sharingResponse{Sharing: sharing, Access: access}, nil
}

// sharingResult is the MCP counterpart of the REST sharing response.
func (s *Server) sharingResult(ctx context.Context, r *http.Request, workbookID string) (sharingResponse, error) {
	sharing, err := s.repository.GetWorkbookSharing(ctx, workbookID)
	if err != nil {
		return sharingResponse{}, err
	}
	access, err := s.repository.ResolveWorkbookAccess(ctx, workbookID, s.accessPrincipal(r))
	if err != nil {
		return sharingResponse{}, err
	}
	return sharingResponse{Sharing: sharing, Access: access}, nil
}

func (s *Server) getWorkbookSharing(w http.ResponseWriter, r *http.Request) {
	response, err := s.sharingWithAccess(r, r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) updateWorkbookSharing(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateSharingInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	workbookID := r.PathValue("workbookId")
	if _, err := s.repository.UpdateWorkbookSharing(r.Context(), workbookID, input); err != nil {
		s.writeError(w, r, err)
		return
	}
	response, err := s.sharingWithAccess(r, workbookID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), workbookID, actorID(r), "")
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) putWorkbookShare(w http.ResponseWriter, r *http.Request) {
	var input workbook.ShareInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	workbookID := r.PathValue("workbookId")
	if _, err := s.repository.PutWorkbookShare(r.Context(), workbookID, input); err != nil {
		s.writeError(w, r, err)
		return
	}
	response, err := s.sharingWithAccess(r, workbookID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), workbookID, actorID(r), "")
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) deleteWorkbookShare(w http.ResponseWriter, r *http.Request) {
	workbookID := r.PathValue("workbookId")
	if err := s.repository.DeleteWorkbookShare(r.Context(), workbookID, r.PathValue("shareId")); err != nil {
		s.writeError(w, r, err)
		return
	}
	response, err := s.sharingWithAccess(r, workbookID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), workbookID, actorID(r), "")
	writeJSON(w, http.StatusOK, response)
}

// transferWorkbookOwnership hands a workbook to a new owner. Only the current
// owner or an administrator reaches this route.
func (s *Server) transferWorkbookOwnership(w http.ResponseWriter, r *http.Request) {
	var input workbook.TransferOwnershipInput
	if !decodeJSON(w, r, &input) {
		return
	}
	access, ok := workbookAccessFrom(r)
	if ok && access.Role != workbook.RoleOwner && access.Source != workbook.AccessSourceAdmin {
		s.writeAccessDenied(w, r, access, workbook.CapabilityManage)
		return
	}
	input.ActorID = actorID(r)
	workbookID := r.PathValue("workbookId")
	if _, err := s.repository.TransferWorkbookOwnership(r.Context(), workbookID, input); err != nil {
		s.writeError(w, r, err)
		return
	}
	response, err := s.sharingWithAccess(r, workbookID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), workbookID, actorID(r), "")
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) listAccessRequests(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListAccessRequests(r.Context(), r.PathValue("workbookId"), strings.TrimSpace(r.URL.Query().Get("status")) == workbook.AccessRequestPending)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// createAccessRequest is deliberately reachable without workbook access: it is
// how a user who followed a restricted link asks the owner for permission.
func (s *Server) createAccessRequest(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateAccessRequestInput
	if !decodeJSON(w, r, &input) {
		return
	}
	principal := s.accessPrincipal(r)
	input.RequesterID = principal.UserID
	input.RequesterMail = principal.Email
	if user, ok := sessionUser(r); ok {
		input.RequesterName = user.DisplayName
	}
	request, err := s.repository.CreateAccessRequest(r.Context(), r.PathValue("workbookId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, request)
}

func (s *Server) decideAccessRequest(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("requestAction")
	separator := strings.LastIndex(action, ":")
	if separator < 0 {
		s.writeError(w, r, workbook.ErrNotFound)
		return
	}
	requestID, decision := action[:separator], action[separator+1:]
	if decision != "approve" && decision != "deny" {
		s.writeError(w, r, workbook.ErrNotFound)
		return
	}
	var input workbook.DecideAccessRequestInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Approve = decision == "approve"
	input.ActorID = actorID(r)
	request, err := s.repository.DecideAccessRequest(r.Context(), requestID, input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), request.WorkbookID, actorID(r), "")
	writeJSON(w, http.StatusOK, request)
}

func (s *Server) listDepartments(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListDepartments(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listMyDepartments(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListDepartmentsForUser(r.Context(), actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createDepartment(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input workbook.CreateDepartmentInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	department, err := s.repository.CreateDepartment(r.Context(), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, department)
}

func (s *Server) getDepartment(w http.ResponseWriter, r *http.Request) {
	department, err := s.repository.GetDepartment(r.Context(), r.PathValue("departmentId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, department)
}

func (s *Server) updateDepartment(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input workbook.UpdateDepartmentInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	department, err := s.repository.UpdateDepartment(r.Context(), r.PathValue("departmentId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, department)
}

func (s *Server) deleteDepartment(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.repository.DeleteDepartment(r.Context(), r.PathValue("departmentId")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addDepartmentMembers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input workbook.DepartmentMembersInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	department, err := s.repository.AddDepartmentMembers(r.Context(), r.PathValue("departmentId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, department)
}

func (s *Server) removeDepartmentMember(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	department, err := s.repository.RemoveDepartmentMember(r.Context(), r.PathValue("departmentId"), r.PathValue("memberId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, department)
}
