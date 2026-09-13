package handoff

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Rejection 은 받는 쪽이 문서를 들이지 않은 까닭이다. 사람에게 보여 줄 문장을
// 들고 있고, 어느 갈래인지는 Code 로 시험이 본다.
type Rejection struct {
	Code    string
	Message string
}

func (r *Rejection) Error() string { return r.Message }

var (
	errSourceNotAllowed = &Rejection{Code: "source_not_allowed", Message: "허용 목록에 없는 곳에서 온 문서입니다. 관리자가 handoff.peers 에 그 서비스를 적어야 받을 수 있습니다."}
	errClaimRejected    = &Rejection{Code: "claim_rejected", Message: "표가 만료되었거나 이미 쓰였습니다. 보낸 쪽에서 다시 보내 주세요."}
	errRedirect         = &Rejection{Code: "redirect", Message: "보낸 쪽이 다른 주소로 넘기려 해 받지 않았습니다."}
	errTooLarge         = &Rejection{Code: "too_large", Message: "문서가 받을 수 있는 크기를 넘어 중간에 끊었습니다."}
	errContentType      = &Rejection{Code: "content_type", Message: "받을 수 없는 형식의 문서입니다. kanpic 은 CSV 와 XLSX 만 받습니다."}
	errUpstream         = &Rejection{Code: "upstream", Message: "보낸 쪽 서비스에서 문서를 받아 오지 못했습니다."}
)

// Fetched 는 허용된 곳에서 받아 온 문서다. Filename 은 형식에 맞는 확장자를
// 달고 있어 그대로 가져오기에 넘길 수 있다.
type Fetched struct {
	Format   string
	Filename string
	Data     []byte
}

// FetchOptions 는 받는 쪽의 상한이다. 비워 두면 표준의 기본값이다.
type FetchOptions struct {
	MaxBytes int64
	Timeout  time.Duration
	// Transport 는 시험이 가짜 서버로 돌리기 위한 자리다.
	Transport http.RoundTripper
}

// Fetch 는 source 에 적힌 곳에서 표를 받아 온다. source 는 밖에서 들어온
// 값이라, 허용 목록에 없으면 아무것도 요청하지 않고 거절한다. 리다이렉트를
// 따라가지 않고, 크기와 시간에 상한을 두며, Content-Type 이 받을 수 있는
// 형식이 아니면 버린다.
func Fetch(ctx context.Context, peers []Peer, source, claim string, options FetchOptions) (Fetched, error) {
	peer, ok := Allowed(peers, source)
	if !ok {
		return Fetched{}, errSourceNotAllowed
	}
	claim = strings.TrimSpace(claim)
	if claim == "" || strings.ContainsAny(claim, "/?#%") {
		return Fetched{}, errClaimRejected
	}
	maxBytes, timeout := options.MaxBytes, options.Timeout
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, peer.Origin+"/api/v1/handoff/claims/"+url.PathEscape(claim), nil)
	if err != nil {
		return Fetched{}, errUpstream
	}
	request.Header.Set("Accept", strings.Join([]string{contentTypes["csv"], contentTypes["xlsx"]}, ", "))
	client := &http.Client{
		Transport: options.Transport,
		// 넘기는 응답은 따라가지 않는다. 마지막 응답을 그대로 받아 아래에서
		// 3xx 로 거절한다.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return Fetched{}, errUpstream
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return Fetched{}, errRedirect
	case response.StatusCode == http.StatusNotFound:
		return Fetched{}, errClaimRejected
	case response.StatusCode != http.StatusOK:
		return Fetched{}, errUpstream
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	format := FormatOf(mediaType)
	if format == "" {
		return Fetched{}, errContentType
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return Fetched{}, &Rejection{Code: "timeout", Message: fmt.Sprintf("보낸 쪽이 %s 안에 문서를 다 보내지 않았습니다.", timeout)}
		}
		return Fetched{}, errUpstream
	}
	if int64(len(data)) > maxBytes {
		return Fetched{}, errTooLarge
	}
	return Fetched{Format: format, Filename: filenameFor(response.Header.Get("Content-Disposition"), format), Data: data}, nil
}

// filenameFor 는 응답이 말한 이름을 형식에 맞는 확장자로 맞춘다. 가져오기는
// 확장자로 파서를 고르므로, Content-Type 이 정한 형식이 이름보다 앞선다.
func filenameFor(disposition, format string) string {
	name := ""
	if _, params, err := mime.ParseMediaType(disposition); err == nil {
		name = strings.TrimSpace(params["filename"])
	}
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		name = "넘겨받은 문서"
	}
	if extension := strings.ToLower(path.Ext(name)); extension == "."+format {
		return name
	} else if extension != "" && (extension == ".csv" || extension == ".xlsx" || extension == ".tsv") {
		name = strings.TrimSuffix(name, path.Ext(name))
	}
	return name + "." + format
}
