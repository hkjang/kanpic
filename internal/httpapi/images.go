package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/workbook"
)

// maxImageBytes reads the administrator's ceiling for one picture.
func (s *Server) maxImageBytes(r *http.Request) int {
	limit := workbook.DefaultMaxImageBytes
	if s.settings == nil {
		return limit
	}
	values, err := s.settings.Values(r.Context())
	if err != nil {
		return limit
	}
	if megabytes, ok := values["files.max_image_mb"].(float64); ok && megabytes >= 1 && megabytes <= 50 {
		return int(megabytes * 1024 * 1024)
	}
	return limit
}

// createImage takes a multipart upload (field "file") and puts the picture on
// the sheet. The type is read from the bytes, never from the file name.
func (s *Server) createImage(w http.ResponseWriter, r *http.Request) {
	sheetID := r.PathValue("sheetId")
	workbookID := s.workbookIDForSheet(r.Context(), sheetID)
	if workbookID == "" {
		s.writeError(w, r, workbook.ErrNotFound)
		return
	}
	limit := s.maxImageBytes(r)
	r.Body = http.MaxBytesReader(w, r.Body, int64(limit)+64*1024)
	if err := r.ParseMultipartForm(int64(limit) + 64*1024); err != nil {
		s.writeError(w, r, fmt.Errorf("%w: image exceeds %d MB or the upload is malformed", workbook.ErrInvalid, limit>>20))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: a file field named \"file\" is required", workbook.ErrInvalid))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: image could not be read", workbook.ErrInvalid))
		return
	}
	input := workbook.CreateImageInput{IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), SheetID: sheetID, Data: data, MaxBytes: limit}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.FormValue("idempotency_key"))
	}
	if x, y := strings.TrimSpace(r.FormValue("x")), strings.TrimSpace(r.FormValue("y")); x != "" || y != "" {
		position := workbook.ChartPosition{}
		position.X, _ = strconv.Atoi(x)
		position.Y, _ = strconv.Atoi(y)
		input.Position = &position
	}
	item, err := s.repository.CreateImage(r.Context(), workbookID, actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListImages(r.Context(), r.PathValue("workbookId"), r.URL.Query().Get("sheet_id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetImage(r.Context(), r.PathValue("imageId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// getImageContent serves the bytes. The type comes from what was sniffed at
// upload, nosniff keeps the browser from second-guessing it, and the picture is
// inline only — never a download prompt, never a script.
func (s *Server) getImageContent(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetImageContent(r.Context(), r.PathValue("imageId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	etag := `"` + item.ID + "-" + strconv.FormatInt(item.Revision, 10) + `"`
	if strings.Contains(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(item.Bytes())))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(item.Bytes())
}

func (s *Server) updateImage(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateImageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateImage(r.Context(), r.PathValue("imageId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	var expected *int64
	if raw := strings.TrimSpace(r.URL.Query().Get("expected_revision")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: expected_revision must be a number", workbook.ErrInvalid))
			return
		}
		expected = &value
	}
	item, err := s.repository.GetImage(r.Context(), r.PathValue("imageId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeleteImage(r.Context(), item.ID, actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), item.WorkbookID, actorID(r), "")
	w.WriteHeader(http.StatusNoContent)
}
