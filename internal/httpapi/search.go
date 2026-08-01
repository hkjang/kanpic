package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/workbook"
)

func (s *Server) searchWorkbook(w http.ResponseWriter, r *http.Request) {
	input := workbook.SearchWorkbookInput{Query: r.URL.Query().Get("q")}
	var err error
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		input.Limit, err = strconv.Atoi(value)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: limit must be an integer", workbook.ErrInvalid))
			return
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("offset")); value != "" {
		input.Offset, err = strconv.Atoi(value)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: offset must be an integer", workbook.ErrInvalid))
			return
		}
	}
	result, err := s.repository.SearchWorkbook(r.Context(), r.PathValue("workbookId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
