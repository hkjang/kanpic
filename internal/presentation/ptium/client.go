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
	var applied struct {
		Data struct {
			Applied      bool               `json:"applied"`
			Warnings     []string           `json:"warnings"`
			Presentation presentationRecord `json:"presentation"`
		} `json:"data"`
	}
	source := map[string]any{"source": WriteSource(request.Deck)}
	if err := c.call(ctx, http.MethodPut, "/api/v1/presentations/"+url.PathEscape(created.Data.ID)+"/source", source, &applied); err != nil {
		return presentation.Result{}, err
	}
	record := applied.Data.Presentation
	if record.ID == "" {
		record = created.Data
	}
	warnings := applied.Data.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return presentation.Result{
		ID: record.ID, Title: record.Title, Status: record.Status,
		SlideCount: record.SlideCount, Template: record.TemplateName,
		EditURL:  c.config.BaseURL + "/presentations/" + record.ID,
		Warnings: warnings, Source: request.Deck.Source,
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
		return nil, "", "", fmt.Errorf("%w: ptium export: %s", presentation.ErrUpstream, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", "", c.statusError("export", response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxExportBytes+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("%w: ptium export: %s", presentation.ErrUpstream, err)
	}
	if len(data) > MaxExportBytes {
		return nil, "", "", fmt.Errorf("%w: ptium export: file is larger than %d bytes", presentation.ErrUpstream, MaxExportBytes)
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
		return fmt.Errorf("%w: ptium %s %s: %s", presentation.ErrUpstream, method, path, err)
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
func (c *Client) statusError(what string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		return fmt.Errorf("%w: ptium %s: %s (%s)", presentation.ErrUpstream, what, envelope.Error.Message, response.Status)
	}
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 200 {
		trimmed = trimmed[:200]
	}
	if trimmed == "" {
		return fmt.Errorf("%w: ptium %s: %s", presentation.ErrUpstream, what, response.Status)
	}
	return fmt.Errorf("%w: ptium %s: %s (%s)", presentation.ErrUpstream, what, trimmed, response.Status)
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
