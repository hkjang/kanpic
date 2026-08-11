package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"kanpic/internal/analytics"
	"kanpic/internal/settings"
)

// cspReportPath is where browsers post the requests the content security
// policy refused. It is unauthenticated because the browser sends it without
// credentials, and it stores nothing but a bounded list of origins in memory.
const cspReportPath = "/api/v1/analytics/csp-report"

// maxReportBytes keeps an unauthenticated endpoint from being used to push
// large bodies at the server.
const maxReportBytes = 8 * 1024

type cspReport struct {
	Report struct {
		BlockedURI         string `json:"blocked-uri"`
		ViolatedDirective  string `json:"violated-directive"`
		EffectiveDirective string `json:"effective-directive"`
		DocumentURI        string `json:"document-uri"`
	} `json:"csp-report"`
}

// receiveCSPReport records what a browser refused to load. Reports are always
// accepted with 204 so a misbehaving page never sees an error from us.
func (s *Server) receiveCSPReport(w http.ResponseWriter, r *http.Request) {
	defer w.WriteHeader(http.StatusNoContent)
	if s.violations == nil {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxReportBytes))
	if err != nil || len(body) == 0 {
		return
	}
	var report cspReport
	if json.Unmarshal(body, &report) != nil {
		return
	}
	directive := report.Report.EffectiveDirective
	if directive == "" {
		directive = report.Report.ViolatedDirective
	}
	s.violations.Record(report.Report.BlockedURI, directive, report.Report.DocumentURI)
}

// listAnalyticsViolations shows the administrator which addresses the policy
// is blocking, so a tracking snippet can be fixed without reading the browser
// console.
func (s *Server) listAnalyticsViolations(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items := []analytics.Violation{}
	if s.violations != nil {
		items = s.violations.List(s.analyticsConfig(r.Context()))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// clearAnalyticsViolations forgets the recorded reports, which is how an
// administrator checks whether a change actually fixed the snippet.
func (s *Server) clearAnalyticsViolations(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.violations != nil {
		s.violations.Forget()
	}
	w.WriteHeader(http.StatusNoContent)
}

// allowAnalyticsHost adds one blocked origin to the tracking allow list. It is
// the one-click fix for the reports listed above.
func (s *Server) allowAnalyticsHost(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct {
		Origin string `json:"origin"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	origin := strings.TrimSpace(input.Origin)
	if origin == "" || !strings.HasPrefix(strings.ToLower(origin), "http") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_origin", "message": "허용할 주소가 올바르지 않습니다."}})
		return
	}
	if s.settings == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "unavailable", "message": "설정 저장소를 사용할 수 없습니다."}})
		return
	}
	current := s.analyticsConfig(r.Context()).AllowedHosts
	hosts, marshalErr := json.Marshal(analytics.AddAllowedHost(current, origin))
	if marshalErr != nil {
		s.writeError(w, r, marshalErr)
		return
	}
	updated, err := s.settings.Put(r.Context(), settings.Setting{Key: "analytics.allowed_hosts", Value: hosts, ValueType: "string"}, actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
