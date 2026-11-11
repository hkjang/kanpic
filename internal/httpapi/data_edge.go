package httpapi

import (
	"net/http"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

// cellDataEdge answers what a Ctrl+arrow walk from one cell needs to know about
// the rest of its row or column. The editor holds only the rows on screen, so
// it asks here before it decides a long column has ended.
func (s *Server) cellDataEdge(w http.ResponseWriter, r *http.Request) {
	selected, err := cellrange.Parse(r.PathValue("address"))
	if err != nil {
		s.writeError(w, r, workbook.ErrInvalid)
		return
	}
	line, err := workbook.ScanDataLine(r.Context(), s.repository, r.PathValue("sheetId"), selected.Start.Row, selected.Start.Column, r.URL.Query().Get("direction"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, line)
}

// cellDataRegion returns the block of data around a cell, read from every
// stored row. Select-all picks it before the whole sheet.
func (s *Server) cellDataRegion(w http.ResponseWriter, r *http.Request) {
	selected, err := cellrange.Parse(r.PathValue("address"))
	if err != nil {
		s.writeError(w, r, workbook.ErrInvalid)
		return
	}
	region, err := workbook.CurrentRegion(r.Context(), s.repository, r.PathValue("sheetId"), selected.Start.Row, selected.Start.Column)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, region)
}
