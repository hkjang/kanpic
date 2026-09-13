package httpapi

import (
	"errors"
	"fmt"
	"html"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"kanpic/internal/auth"
	"kanpic/internal/handoff"
	"kanpic/internal/importexport"
	"kanpic/internal/workbook"
)

// 서비스 간 문서 넘기기(HANDOFF-STANDARD). 엔드포인트 이름·요청 모양·응답
// 코드는 표준 그대로다 — 여섯 서비스가 서로 맞물려야 하므로 한 곳이라도
// 다르면 아무것도 이어지지 않는다.

const handoffClaimsPath = "/api/v1/handoff/claims/"

// WithHandoff 는 문서 넘기기를 잇는다. 없으면 허용 목록이 빈 것과 같이
// 동작한다 — 보내기 단추가 없고 아무 데서도 받지 않는다.
func WithHandoff(service *handoff.Service) PlatformOption {
	return func(s *Server) { s.handoff = service }
}

// handoffTargets 는 편집기가 보내기 단추를 그릴지 묻는 자리다. 허용 목록에
// 없거나 kanpic 이 보내는 형식을 받지 못하는 서비스는 목록에 없다.
func (s *Server) handoffTargets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"source": s.handoffSource(r), "targets": s.handoff.Targets(r.Context())})
}

// handoffSource 는 표에 적는 이 서비스의 주소다. 받는 쪽은 이 주소로 표를
// 받으러 오므로 프록시 바깥에서 닿는 주소여야 한다.
func (s *Server) handoffSource(r *http.Request) string {
	if public := s.handoff.PublicURL(r.Context()); public != "" {
		return public
	}
	return auth.RequestOrigin(r)
}

// createHandoffClaim 은 POST /api/v1/handoff/claims 다. 표는 그 사용자가 읽을
// 수 있는 그 문서 하나에만 묶인다 — 내보내기와 같은 권한 검사를 지난다.
func (s *Server) createHandoffClaim(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Resource string `json:"resource"`
		Format   string `json:"format"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	format := strings.ToLower(strings.TrimSpace(input.Format))
	if handoff.ContentType(format) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "unsupported_format", "message": "kanpic 은 csv 와 xlsx 만 보냅니다."}})
		return
	}
	// resource 는 워크북 id, 또는 시트 하나를 보낼 때 "<워크북>/sheets/<시트>".
	workbookID, sheetID, _ := strings.Cut(strings.TrimSpace(input.Resource), "/sheets/")
	access, allowed := s.authorizeWorkbookID(w, r, workbookID, workbook.CapabilityRead)
	if !allowed {
		return
	}
	if !access.CanCopy {
		s.writeCopyDenied(w, r, access)
		return
	}
	exported, err := s.files.Export(r.Context(), importexport.ExportRequest{WorkbookID: workbookID, SheetID: sheetID, Format: format})
	if err != nil {
		s.fileError(w, err)
		return
	}
	contentType := handoff.ContentType(format)
	if format == "csv" {
		contentType += "; charset=utf-8"
	}
	claim, ticket, err := s.handoff.Issue(r.Context(), handoff.Ticket{
		Resource: input.Resource, Format: format, Filename: exported.Name,
		ContentType: contentType, Data: exported.Data, IssuedBy: actorID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"claim": claim, "source": s.handoffSource(r), "filename": ticket.Filename,
		"content_type": ticket.ContentType, "bytes": len(ticket.Data), "expires_at": ticket.ExpiresAt,
	})
}

// serveHandoffClaim 은 GET /api/v1/handoff/claims/{claim} 이다. 로그인이 필요
// 없다 — 표가 곧 자격이다. 이미 쓴 표나 시간이 지난 표는 404 로 답하고 왜
// 거절됐는지 구별해 주지 않는다.
func (s *Server) serveHandoffClaim(w http.ResponseWriter, r *http.Request) {
	ticket, err := s.handoff.Redeem(r.Context(), r.PathValue("claim"))
	if err != nil {
		if !errors.Is(err, handoff.ErrNotFound) {
			s.logger.Error("handoff claim lookup failed", "error", err)
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found", "message": "표가 없거나 이미 쓰였거나 시간이 지났습니다."}})
		return
	}
	contentType := ticket.ContentType
	if contentType == "" {
		contentType = handoff.ContentType(ticket.Format)
	}
	w.Header().Set("Content-Type", contentType)
	// 표준의 예시는 UTF-8'' 이다. Go 는 소문자로 적고 둘 다 규격에 맞지만,
	// 여섯 서비스가 저마다 다른 파서를 쓰므로 예시와 글자까지 같게 둔다.
	w.Header().Set("Content-Disposition", strings.Replace(mime.FormatMediaType("attachment", map[string]string{"filename": ticket.Filename}), "filename*=utf-8''", "filename*=UTF-8''", 1))
	w.Header().Set("Content-Length", fmt.Sprint(len(ticket.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(ticket.Data)
}

// receiveHandoff 는 GET /handoff?source=&claim= 이다. 브라우저가 표를 들고
// 온다. source 는 밖에서 들어온 값이라 허용 목록에 없으면 아무것도 요청하지
// 않고 거절한다. 들인 문서는 워크북이 되고, 어디서 왔는지가 남는다.
func (s *Server) receiveHandoff(w http.ResponseWriter, r *http.Request) {
	source, claim := strings.TrimSpace(r.URL.Query().Get("source")), strings.TrimSpace(r.URL.Query().Get("claim"))
	if source == "" || claim == "" {
		s.handoffPage(w, http.StatusBadRequest, "주소에 source 와 claim 이 있어야 합니다.")
		return
	}
	if _, ok := sessionUser(r); !ok && s.loginRequired(r) {
		// 표는 5분짜리다. 로그인하고 돌아오면 같은 주소로 다시 들어온다.
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	fetched, err := s.handoff.Fetch(r.Context(), source, claim)
	if err != nil {
		var rejection *handoff.Rejection
		if errors.As(err, &rejection) {
			s.handoffPage(w, http.StatusBadGateway, rejection.Message)
			return
		}
		s.handoffPage(w, http.StatusBadGateway, "문서를 받아 오지 못했습니다.")
		return
	}
	parsed, err := importexport.Parse(fetched.Filename, fetched.Data, s.maxExpandedBytes(r))
	if err != nil {
		s.handoffPage(w, http.StatusUnprocessableEntity, "받은 문서를 읽을 수 없습니다: "+err.Error())
		return
	}
	actor := actorID(r)
	created, err := s.repository.ImportWorkbook(r.Context(), workbook.ImportWorkbookInput{
		Title: parsed.Title, OwnerID: actor, ActorID: actor, IdempotencyKey: "handoff:" + handoff.HashClaim(claim),
		FileName: fetched.Filename, Format: parsed.Format, Sheets: parsed.Sheets, NamedRanges: parsed.NamedRanges, NamedFunctions: parsed.NamedFunctions,
	})
	if err != nil {
		s.logger.Error("handoff import failed", "error", err, "source", source)
		s.handoffPage(w, http.StatusInternalServerError, "받은 문서로 워크북을 만들지 못했습니다.")
		return
	}
	normalized, _ := handoff.NormalizeOrigin(source)
	if err := s.handoff.Record(r.Context(), handoff.Receipt{WorkbookID: created.ID, Source: normalized, Filename: fetched.Filename, ReceivedBy: actor}); err != nil {
		s.logger.Error("handoff receipt not saved", "error", err, "workbook_id", created.ID)
	}
	http.Redirect(w, r, "/workbooks/"+url.PathEscape(created.ID), http.StatusFound)
}

// handoffOrigin 은 GET /api/v1/workbooks/{workbookId}/handoff 다. 넘겨받은
// 워크북이 어디서 왔는지 답한다. 넘겨받은 것이 아니면 404.
func (s *Server) handoffOrigin(w http.ResponseWriter, r *http.Request) {
	receipt, err := s.handoff.Origin(r.Context(), r.PathValue("workbookId"))
	if errors.Is(err, handoff.ErrNotFound) {
		s.writeError(w, r, workbook.ErrNotFound)
		return
	}
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

// loginRequired 는 이 설치가 로그인을 요구하는지다. 개방형 초기 설정 모드에서는
// 요구하지 않는다.
func (s *Server) loginRequired(r *http.Request) bool {
	if s.auth == nil {
		return false
	}
	if s.auth.BootstrapEnabled() {
		return true
	}
	config, err := s.auth.Config(r.Context())
	return err == nil && config.Enabled
}

// handoffPage 는 실패했을 때 사람이 읽을 수 있는 화면이다. 앱의 페이지 정책이
// 인라인 스타일을 막으므로 꾸미지 않는다.
func (s *Server) handoffPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="ko"><head><meta charset="utf-8"><title>문서를 받지 못했습니다</title></head><body><main><h1>문서를 받지 못했습니다</h1><p>%s</p><p><a href="/">홈으로</a></p></main></body></html>`, html.EscapeString(message))
}

// loggedPath 는 요청 로그에 적는 경로다. 표는 로그에 남지 않는다 — 표가 곧
// 자격이므로 로그를 읽을 수 있는 사람이 문서도 받아 갈 수 있으면 안 된다.
func loggedPath(path string) string {
	if strings.HasPrefix(path, handoffClaimsPath) && len(path) > len(handoffClaimsPath) {
		return handoffClaimsPath + "{claim}"
	}
	return path
}
