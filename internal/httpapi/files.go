package httpapi

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"kanpic/internal/importexport"
	"kanpic/internal/workbook"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) previewImport(w http.ResponseWriter, r *http.Request) {
	fileName, data, _, err := s.readUpload(w, r)
	if err != nil {
		s.fileError(w, err)
		return
	}
	preview, err := s.files.Preview(r.Context(), fileName, data, s.maxExpandedBytes(r))
	if err != nil {
		s.fileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) executeImport(w http.ResponseWriter, r *http.Request) {
	fileName, data, workspaceID, err := s.readUpload(w, r)
	if err != nil {
		s.fileError(w, err)
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		s.fileError(w, errors.New("Idempotency-Key header is required"))
		return
	}
	created, err := s.files.Import(r.Context(), importexport.ImportRequest{FileName: fileName, Data: data, WorkspaceID: workspaceID, ActorID: actorID(r), IdempotencyKey: idempotencyKey, MaxExpandedBytes: s.maxExpandedBytes(r)})
	if err != nil {
		s.fileError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) executeExport(w http.ResponseWriter, r *http.Request) {
	var input importexport.ExportRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	// Exporting is a copy, so viewers follow the workbook download policy.
	access, allowed := s.authorizeWorkbookID(w, r, input.WorkbookID, workbook.CapabilityRead)
	if !allowed {
		return
	}
	if !access.CanCopy {
		s.writeCopyDenied(w, r, access)
		return
	}
	exported, err := s.files.Export(r.Context(), input)
	if err != nil {
		s.fileError(w, err)
		return
	}
	w.Header().Set("Content-Type", exported.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": exported.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 엑셀에 같은 그림이 없어 담지 못한 차트가 있으면 몇 개인지 알린다.
	// 조용히 빠뜨리면 사람은 파일을 열어 보고서야 알게 되고, 그때는 이미
	// 그 파일을 남에게 보낸 뒤다. 문구는 화면 쪽에 둔다 — 헤더에 한글을
	// 실으면 인코딩을 따로 다뤄야 한다.
	if exported.SkippedCharts > 0 {
		w.Header().Set("X-Kanpic-Skipped-Charts", strconv.Itoa(exported.SkippedCharts))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(exported.Data)
}

func (s *Server) readUpload(w http.ResponseWriter, r *http.Request) (string, []byte, string, error) {
	maxBytes := s.maxUploadBytes(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		return "", nil, "", fmt.Errorf("invalid multipart upload: %w", err)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return "", nil, "", errors.New("file form field is required")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return "", nil, "", fmt.Errorf("file exceeds the %d byte upload limit", maxBytes)
	}
	fileName := strings.TrimSpace(header.Filename)
	if fileName == "" {
		return "", nil, "", errors.New("file name is required")
	}
	return fileName, data, r.FormValue("workspace_id"), nil
}

func (s *Server) maxUploadBytes(r *http.Request) int64 {
	limit := int64(importexport.DefaultMaxUploadBytes)
	if s.settings == nil {
		return limit
	}
	values, err := s.settings.Values(r.Context())
	if err != nil {
		return limit
	}
	if megabytes, ok := values["files.max_import_mb"].(float64); ok && megabytes >= 1 && megabytes <= 2048 {
		return int64(megabytes * 1024 * 1024)
	}
	return limit
}
func (s *Server) maxExpandedBytes(r *http.Request) int64 {
	limit := s.maxUploadBytes(r) * 10
	if limit < importexport.DefaultMaxExpandedBytes {
		return importexport.DefaultMaxExpandedBytes
	}
	return limit
}
func (s *Server) fileError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, workbook.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": "file_operation_failed", "message": err.Error()}})
}

func encodeExportForMCP(file importexport.ExportedFile) map[string]any {
	return map[string]any{"file_name": file.Name, "content_type": file.ContentType, "data_base64": base64.StdEncoding.EncodeToString(file.Data), "size_bytes": len(file.Data)}
}
