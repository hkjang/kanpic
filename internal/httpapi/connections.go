package httpapi

import "net/http"

// listConnections reports the workbook's IMPORTRANGE targets and whether each
// one can be read right now, which is the only way to tell a stale value from
// a permission that was taken away.
func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	result, err := s.repository.ListConnections(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// refreshConnections recalculates the workbook so every IMPORTRANGE re-reads
// its source. It bumps the workbook version, so collaborators pick the new
// values up the way they pick up any other change.
func (s *Server) refreshConnections(w http.ResponseWriter, r *http.Request) {
	workbookID := r.PathValue("workbookId")
	result, err := s.repository.RefreshConnections(r.Context(), workbookID, actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(workbookID, actorID(r), r.Header.Get("X-Kanpic-Client"), "", result.Version)
	writeJSON(w, http.StatusOK, result)
}
