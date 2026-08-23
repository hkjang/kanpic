package httpapi

import "net/http"

// printFramePath serves the empty page a print document is written into.
//
// The application's own policy forbids inline styles, which is what keeps a
// pasted tracking snippet from restyling the page. A printed sheet, though, is
// nothing but per-cell style: this cell is red, that one is bold, this column
// is 140 pixels wide. Written into a blank frame, all of it was silently
// dropped and every sheet printed as unstyled text.
//
// A document loaded from a URL carries the policy of its own response rather
// than its parent's, so this one page allows styling — and nothing else. No
// script, no network, no images beyond what the document carries itself. The
// only thing it can do is draw on paper.
const printFramePath = "/print-frame"

const printFrameDocument = `<!doctype html><html lang="ko"><head><meta charset="utf-8"><title>인쇄</title></head><body></body></html>`

func (s *Server) printFrame(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(printFrameDocument))
}
