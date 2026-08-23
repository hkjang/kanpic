package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"kanpic/internal/presentation"
)

// The presentation routes are a gateway rather than a passthrough: the browser
// never talks to the presentation service and never sees its API key. A person
// asks kanpic for a deck of a range they may read, and kanpic asks the service.

type presentationCreateRequest struct {
	Range        string `json:"range"`
	Title        string `json:"title,omitempty"`
	Language     string `json:"language,omitempty"`
	TemplateID   string `json:"template_id,omitempty"`
	IncludeTable *bool  `json:"include_table,omitempty"`
	Preview      bool   `json:"preview,omitempty"`
}

func (s *Server) presentationConfig(w http.ResponseWriter, r *http.Request) {
	if s.presentations == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	config, available := s.presentations.Available(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":             available,
		"provider":            config.Provider,
		"max_cells":           config.MaxCells,
		"default_template_id": config.TemplateID,
	})
}

func (s *Server) presentationTemplates(w http.ResponseWriter, r *http.Request) {
	if s.presentations == nil {
		s.writeError(w, r, presentation.ErrNotConfigured)
		return
	}
	templates, err := s.presentations.Templates(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": templates})
}

func (s *Server) createPresentation(w http.ResponseWriter, r *http.Request) {
	if s.presentations == nil {
		s.writeError(w, r, presentation.ErrNotConfigured)
		return
	}
	var request presentationCreateRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	includeTable := true
	if request.IncludeTable != nil {
		includeTable = *request.IncludeTable
	}
	input := presentation.CreateRequestInput{
		ActorID: actorID(r), SheetID: r.PathValue("sheetId"), Range: request.Range, Title: request.Title,
		Language: request.Language, TemplateID: request.TemplateID, IncludeTable: includeTable,
	}
	// 미리보기는 프레젠테이션 서비스를 부르지 않는다. 같은 코드가 같은 덱을
	// 만들므로, 보이는 것과 만들어지는 것이 다를 수 없다.
	if request.Preview {
		deck, analysis, err := s.presentations.Preview(r.Context(), input)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deck": deck, "analysis": summarizeAnalysis(analysis)})
		return
	}
	result, analysis, err := s.presentations.Create(r.Context(), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"presentation": result, "analysis": summarizeAnalysis(analysis)})
}

func (s *Server) exportPresentation(w http.ResponseWriter, r *http.Request) {
	if s.presentations == nil {
		s.writeError(w, r, presentation.ErrNotConfigured)
		return
	}
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	data, contentType, filename, err := s.presentations.Export(r.Context(), r.PathValue("presentationId"), format)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if strings.TrimSpace(filename) == "" {
		filename = "presentation.pptx"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	// 한글 제목은 filename* 로 보내야 브라우저가 이름을 살린다.
	w.Header().Set("Content-Disposition", "attachment; filename=\"presentation.pptx\"; filename*=UTF-8''"+url.PathEscape(filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// summarizeAnalysis is what the dialog shows about the range: what kanpic
// decided it is, so a person can tell at a glance whether it read their table
// the way they meant it.
func summarizeAnalysis(analysis presentation.Analysis) map[string]any {
	columns := make([]map[string]any, 0, len(analysis.Columns))
	for _, column := range analysis.Columns {
		columns = append(columns, map[string]any{"name": column.Name, "kind": column.Kind, "role": column.Role})
	}
	return map[string]any{
		"shape": analysis.Shape, "chart": analysis.Chart, "row_count": analysis.RowCount,
		"has_header": analysis.HasHeader, "headline": analysis.Headline, "columns": columns,
	}
}

func (s *Server) listWorkbookPresentations(w http.ResponseWriter, r *http.Request) {
	if s.presentations == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	records, err := s.presentations.ListForWorkbook(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": records})
}
