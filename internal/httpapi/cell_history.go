package httpapi

import (
	"net/http"
	"strconv"

	"kanpic/pkg/cellrange"
)

// cellHistory answers "who changed this cell, when, and what did it say
// before". The workbook version list already tells the story of the workbook;
// this tells the story of one cell, which is what people actually chase.
func (s *Server) cellHistory(w http.ResponseWriter, r *http.Request) {
	sheetID := r.PathValue("sheetId")
	selected, err := cellrange.Parse(r.PathValue("address"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	history, err := s.repository.CellHistory(r.Context(), sheetID, selected.Start.Row, selected.Start.Column, limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	ids := make([]string, 0, len(history.Items))
	seen := make(map[string]bool, len(history.Items))
	for _, item := range history.Items {
		if item.ActorID != "" && !seen[item.ActorID] {
			seen[item.ActorID] = true
			ids = append(ids, item.ActorID)
		}
	}
	if len(ids) > 0 {
		if users, lookupErr := s.repository.LookupUsers(r.Context(), ids); lookupErr == nil {
			names := make(map[string]string, len(users))
			for _, user := range users {
				if user.DisplayName != "" {
					names[user.UserID] = user.DisplayName
				}
			}
			for index := range history.Items {
				history.Items[index].ActorName = names[history.Items[index].ActorID]
			}
		}
	}
	writeJSON(w, http.StatusOK, history)
}
