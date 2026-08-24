package ptium

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kanpic/internal/presentation"
)

// MaxExportBytes bounds a downloaded deck. A PPTX of a dozen slides is a few
// hundred kilobytes; anything approaching this is a misconfigured endpoint
// rather than a presentation.
const MaxExportBytes = 64 << 20

type Config struct {
	BaseURL    string
	APIKey     string
	Timeout    time.Duration
	TemplateID string
}

type Client struct {
	config Config
	http   *http.Client
}

func New(config Config, client *http.Client) *Client {
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	if client == nil {
		client = &http.Client{}
	}
	client.Timeout = config.Timeout
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	return &Client{config: config, http: client}
}

func (c *Client) Name() string { return "ptium" }

type envelope[T any] struct {
	Data T `json:"data"`
}

type presentationRecord struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	TemplateID   string `json:"templateId"`
	TemplateName string `json:"templateName"`
	SlideCount   int    `json:"slideCount"`
	Version      int64  `json:"version"`
}

type templateRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BuiltIn bool   `json:"builtIn"`
}

func (c *Client) Templates(ctx context.Context) ([]presentation.Template, error) {
	var decoded struct {
		Data []templateRecord `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/templates?limit=100", nil, &decoded); err != nil {
		return nil, err
	}
	templates := make([]presentation.Template, 0, len(decoded.Data))
	for _, record := range decoded.Data {
		templates = append(templates, presentation.Template{ID: record.ID, Name: record.Name, BuiltIn: record.BuiltIn})
	}
	return templates, nil
}

// Create makes the deck in two steps on purpose: a draft carries the title and
// the template, and applying source compiles the slides. Ptium's one-shot
// endpoint asks a language model to write the deck from a prompt, which is the
// wrong tool here — kanpic already knows what the slides say, and a model asked
// to restate known numbers is a chance to get them wrong.
func (c *Client) Create(ctx context.Context, request presentation.CreateRequest) (presentation.Result, error) {
	body := map[string]any{"title": request.Deck.Title}
	if language := strings.TrimSpace(request.Deck.Language); language != "" {
		body["language"] = language
	}
	if template := strings.TrimSpace(request.TemplateID); template != "" {
		body["templateId"] = template
	} else if strings.TrimSpace(c.config.TemplateID) != "" {
		body["templateId"] = strings.TrimSpace(c.config.TemplateID)
	}
	var created envelope[presentationRecord]
	if err := c.call(ctx, http.MethodPost, "/api/v1/presentations", body, &created); err != nil {
		return presentation.Result{}, err
	}
	return c.applySource(ctx, created.Data, request.Deck)
}

// Replace recompiles an existing deck from a fresh reading of its range. The
// deck keeps its id, so a link already shared keeps working and shows the
// current numbers.
func (c *Client) Replace(ctx context.Context, id string, deck presentation.Deck) (presentation.Result, error) {
	if strings.TrimSpace(id) == "" {
		return presentation.Result{}, fmt.Errorf("%w: ptium replace: presentation id is required", presentation.ErrUpstream)
	}
	// 제목도 함께 맞춘다. 범위 이름이 바뀌었는데 표지만 옛말을 하면 안 된다.
	var updated envelope[presentationRecord]
	if err := c.call(ctx, http.MethodPut, "/api/v1/presentations/"+url.PathEscape(id), map[string]any{"title": deck.Title}, &updated); err != nil {
		return presentation.Result{}, err
	}
	record := updated.Data
	if record.ID == "" {
		record.ID = id
	}
	return c.applySource(ctx, record, deck)
}

// applySource compiles the deck into the presentation's slides. Ptium reports
// every adjustment it had to make in warnings, and those are carried back
// rather than dropped.
func (c *Client) applySource(ctx context.Context, target presentationRecord, deck presentation.Deck) (presentation.Result, error) {
	var applied struct {
		Data struct {
			Applied      bool               `json:"applied"`
			Warnings     []string           `json:"warnings"`
			Presentation presentationRecord `json:"presentation"`
		} `json:"data"`
	}
	source := map[string]any{"source": WriteSource(deck)}
	if err := c.call(ctx, http.MethodPut, "/api/v1/presentations/"+url.PathEscape(target.ID)+"/source", source, &applied); err != nil {
		return presentation.Result{}, err
	}
	record := applied.Data.Presentation
	if record.ID == "" {
		record = target
	}
	warnings := applied.Data.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return presentation.Result{
		ID: record.ID, Title: record.Title, Status: record.Status,
		SlideCount: record.SlideCount, Template: record.TemplateName,
		// 편집 화면은 /editor 까지 가야 열린다. 여기까지만 적어 보내면
		// 보기 화면이 떠서, 사람이 고치려고 눌렀는데 고칠 수가 없다.
		EditURL:  c.config.BaseURL + "/presentations/" + url.PathEscape(record.ID) + "/editor",
		Warnings: warnings, Source: deck.Source,
	}, nil
}

func (c *Client) Export(ctx context.Context, id, format string) ([]byte, string, string, error) {
	if strings.TrimSpace(format) == "" {
		format = "pptx"
	}
	target := c.config.BaseURL + "/api/v1/presentations/" + url.PathEscape(id) + "/export?format=" + url.QueryEscape(format)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", "", err
	}
	c.authorize(request)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, "", "", unreachable("export", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", "", c.statusError("export", response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxExportBytes+1))
	if err != nil {
		return nil, "", "", unreachable("export", err)
	}
	if len(data) > MaxExportBytes {
		return nil, "", "", &presentation.UpstreamError{
			Summary: "프레젠테이션 파일이 너무 큽니다.",
			Detail:  fmt.Sprintf("ptium export: file is larger than %d bytes", MaxExportBytes),
		}
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	}
	return data, contentType, filenameFrom(response.Header.Get("Content-Disposition")), nil
}

func filenameFrom(disposition string) string {
	if disposition == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		return ""
	}
	return params["filename"]
}

func (c *Client) call(ctx context.Context, method, path string, body any, into any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.config.BaseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	c.authorize(request)
	response, err := c.http.Do(request)
	if err != nil {
		return unreachable(method+" "+path, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.statusError(method+" "+path, response)
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(into)
}

// authorize sends the API key the way Ptium documents. The key never leaves the
// server: a browser asks kanpic, and kanpic asks Ptium.
func (c *Client) authorize(request *http.Request) {
	key := strings.TrimSpace(c.config.APIKey)
	if key == "" {
		return
	}
	if strings.HasPrefix(key, "dev:") {
		// 개발용 공유 비밀. 운영에서는 ptium_ 로 시작하는 API Key 를 쓴다.
		request.Header.Set("X-Ptium-Dev-Secret", strings.TrimPrefix(key, "dev:"))
		return
	}
	request.Header.Set("Authorization", "Bearer "+key)
}

// statusError reads Ptium's error envelope so the person who pressed the button
// sees what Ptium said rather than a bare status code.
// unreachable is a failure to talk to the service at all. The address belongs
// in the log; the reader is told which side is at fault and nothing more.
func unreachable(what string, err error) *presentation.UpstreamError {
	return &presentation.UpstreamError{
		Summary: "프레젠테이션 서비스에 연결하지 못했습니다. 관리자에게 문의하세요.",
		Detail:  fmt.Sprintf("ptium %s: %v", what, err),
	}
}

func (c *Client) statusError(what string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	message := ""
	if json.Unmarshal(body, &envelope) == nil {
		message = strings.TrimSpace(envelope.Error.Message)
	}
	detail := fmt.Sprintf("ptium %s: %s", what, response.Status)
	if message != "" {
		detail += ": " + message
	} else if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
		if len(trimmed) > 200 {
			trimmed = trimmed[:200]
		}
		detail += ": " + trimmed
	}
	summary := ""
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		// 열쇠 문제는 사용자가 고칠 수 없다. 무엇이 문제인지만 말한다.
		summary = "프레젠테이션 서비스가 인증을 거부했습니다. 관리자에게 문의하세요."
	case response.StatusCode >= 500:
		summary = "프레젠테이션 서비스에 오류가 발생했습니다."
	case message != "":
		// 서비스가 덱에 대해 한 말은 대개 가장 쓸모 있는 문장이다.
		summary = "프레젠테이션 서비스가 요청을 거절했습니다: " + message
	default:
		summary = "프레젠테이션 서비스가 요청을 거절했습니다."
	}
	return &presentation.UpstreamError{Summary: summary, Detail: detail}
}

var _ presentation.Provider = (*Client)(nil)

// Factory builds the client from kanpic's stored configuration. Registering it
// is the only line anywhere in kanpic that names Ptium.
func Factory(config presentation.Config) (presentation.Provider, error) {
	return New(Config{
		BaseURL:    config.BaseURL,
		APIKey:     config.APIKey,
		Timeout:    config.Timeout,
		TemplateID: config.TemplateID,
	}, nil), nil
}
