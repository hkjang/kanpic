package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kanpic/internal/presentation"
	"kanpic/internal/workbook"
)

// recordingProvider stands in for the presentation service. The tests here are
// about what kanpic does around the call — who may make a deck, who may
// download one — so the deck itself only has to come back with an id.
type recordingProvider struct {
	decks    []presentation.Deck
	replaced []presentation.Deck
	exported []string
}

func (p *recordingProvider) Name() string { return "fake" }

func (p *recordingProvider) Templates(context.Context) ([]presentation.Template, error) {
	return []presentation.Template{{ID: "template-1", Name: "기본"}}, nil
}

func (p *recordingProvider) Create(_ context.Context, request presentation.CreateRequest) (presentation.Result, error) {
	p.decks = append(p.decks, request.Deck)
	return presentation.Result{ID: "deck-1", Title: request.Deck.Title, Status: "completed", SlideCount: len(request.Deck.Slides), Warnings: []string{}}, nil
}

func (p *recordingProvider) Replace(_ context.Context, id string, deck presentation.Deck) (presentation.Result, error) {
	p.replaced = append(p.replaced, deck)
	return presentation.Result{ID: id, Title: deck.Title, Status: "completed", SlideCount: len(deck.Slides), Warnings: []string{}}, nil
}

func (p *recordingProvider) Export(_ context.Context, id, _ string) ([]byte, string, string, error) {
	p.exported = append(p.exported, id)
	return []byte("PK-fake-pptx"), "application/vnd.openxmlformats-officedocument.presentationml.presentation", "덱.pptx", nil
}

type fixedSettings map[string]any

func (f fixedSettings) Values(context.Context) (map[string]any, error) { return f, nil }

func presentationServer(t *testing.T) (*httptest.Server, *recordingProvider, string) {
	t.Helper()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "실적", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"부서"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"영업1"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`120`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"영업2"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`95`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`"영업3"`)}, {Row: 4, Column: 2, Value: json.RawMessage(`110`)},
	}}); err != nil {
		t.Fatal(err)
	}
	provider := &recordingProvider{}
	service := presentation.NewService(
		fixedSettings{"presentation.enabled": true, "presentation.provider": "fake", "presentation.base_url": "http://presentation.invalid"},
		repository, presentation.NewMemoryStore(),
		map[string]presentation.Factory{"fake": func(presentation.Config) (presentation.Provider, error) { return provider, nil }},
	)
	handler := NewPlatformWithServices(repository, nil, nil, nil, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithPresentations(service))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, provider, sheet.ID
}

func TestPresentationIsMadeFromWhatTheRangeMeans(t *testing.T) {
	t.Parallel()
	server, provider, sheetID := presentationServer(t)
	created := requestAs[struct {
		Presentation presentation.Result `json:"presentation"`
		Analysis     struct {
			Shape string `json:"shape"`
			Chart string `json:"chart"`
		} `json:"analysis"`
	}](t, server, "alice", http.MethodPost, "/api/v1/sheets/"+sheetID+"/presentations",
		map[string]any{"range": "A1:B4", "title": "부서별 매출"}, http.StatusCreated)
	if created.Presentation.ID != "deck-1" || created.Analysis.Chart != "bars" {
		t.Fatalf("created=%+v analysis=%+v", created.Presentation, created.Analysis)
	}
	// 프레젠테이션 서비스는 셀이 아니라 뜻을 받는다.
	if len(provider.decks) != 1 {
		t.Fatalf("provider saw %d decks", len(provider.decks))
	}
	deck := provider.decks[0]
	if deck.Title != "부서별 매출" || deck.Source.Range != "A1:B4" || deck.Source.SheetID != sheetID {
		t.Fatalf("deck=%+v", deck)
	}
	if len(deck.Slides) < 2 {
		t.Fatalf("slides=%+v", deck.Slides)
	}
}

// 미리보기는 서비스를 부르지 않는다. 고르는 중에 남의 계정에 덱이 쌓이면
// 안 된다.
func TestPresentationPreviewAsksNobody(t *testing.T) {
	t.Parallel()
	server, provider, sheetID := presentationServer(t)
	previewed := requestAs[struct {
		Deck presentation.Deck `json:"deck"`
	}](t, server, "alice", http.MethodPost, "/api/v1/sheets/"+sheetID+"/presentations",
		map[string]any{"range": "A1:B4", "preview": true}, http.StatusOK)
	if len(previewed.Deck.Slides) == 0 {
		t.Fatalf("preview=%+v", previewed.Deck)
	}
	if len(provider.decks) != 0 {
		t.Fatalf("preview reached the provider %d times", len(provider.decks))
	}
}

// 덱은 공용 계정 아래 만들어진다. kanpic 이 어느 워크북에서 나왔는지 기억하지
// 않으면 로그인한 누구나 남의 자료로 만든 덱을 내려받을 수 있다.
func TestPresentationDownloadFollowsTheWorkbook(t *testing.T) {
	t.Parallel()
	server, provider, sheetID := presentationServer(t)
	created := requestAs[struct {
		Presentation presentation.Result `json:"presentation"`
	}](t, server, "alice", http.MethodPost, "/api/v1/sheets/"+sheetID+"/presentations",
		map[string]any{"range": "A1:B4"}, http.StatusCreated)
	id := created.Presentation.ID

	response := doRequest(t, server, "alice", http.MethodGet, "/api/v1/presentations/"+id+"/export")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owner download = %d", response.StatusCode)
	}
	response.Body.Close()

	for name, path := range map[string]string{
		"a stranger":      "/api/v1/presentations/" + id + "/export",
		"an unknown deck": "/api/v1/presentations/deck-nobody-made/export",
	} {
		actor := "mallory"
		if name == "an unknown deck" {
			actor = "alice"
		}
		refused := doRequest(t, server, actor, http.MethodGet, path)
		refused.Body.Close()
		if refused.StatusCode == http.StatusOK {
			t.Fatalf("%s downloaded the deck", name)
		}
	}
	if len(provider.exported) != 1 {
		t.Fatalf("the provider was asked for %d exports", len(provider.exported))
	}
}

func TestPresentationRefusesARangeItCannotRead(t *testing.T) {
	t.Parallel()
	server, _, sheetID := presentationServer(t)
	requestAs[map[string]any](t, server, "mallory", http.MethodPost, "/api/v1/sheets/"+sheetID+"/presentations",
		map[string]any{"range": "A1:B4"}, http.StatusForbidden)
}

func TestPresentationListRemembersWhereADeckCameFrom(t *testing.T) {
	t.Parallel()
	server, _, sheetID := presentationServer(t)
	created := requestAs[struct {
		Presentation presentation.Result `json:"presentation"`
	}](t, server, "alice", http.MethodPost, "/api/v1/sheets/"+sheetID+"/presentations",
		map[string]any{"range": "A1:B4", "title": "기록"}, http.StatusCreated)
	book := requestAs[struct {
		Items []presentation.Record `json:"items"`
	}](t, server, "alice", http.MethodGet, "/api/v1/workbooks/"+workbookOf(t, server, sheetID)+"/presentations", nil, http.StatusOK)
	if len(book.Items) != 1 {
		t.Fatalf("records=%+v", book.Items)
	}
	record := book.Items[0]
	if record.ID != created.Presentation.ID || record.Range != "A1:B4" || record.SourceVersion == 0 {
		t.Fatalf("record=%+v", record)
	}
}

// 원본이 바뀌면 덱은 옛말을 한다. 다시 만들기는 **그때 그 범위** 를 지금
// 상태로 다시 읽는 것이지, 부르는 쪽이 준 범위로 바꾸는 것이 아니다.
func TestPresentationRefreshRereadsTheRangeItWasMadeFrom(t *testing.T) {
	t.Parallel()
	server, provider, sheetID := presentationServer(t)
	created := requestAs[struct {
		Presentation presentation.Result `json:"presentation"`
	}](t, server, "alice", http.MethodPost, "/api/v1/sheets/"+sheetID+"/presentations",
		map[string]any{"range": "A1:B4", "title": "부서별 매출"}, http.StatusCreated)
	book := workbookOf(t, server, sheetID)

	listed := func() presentation.Record {
		t.Helper()
		page := requestAs[struct {
			Items []presentation.Record `json:"items"`
		}](t, server, "alice", http.MethodGet, "/api/v1/workbooks/"+book+"/presentations", nil, http.StatusOK)
		if len(page.Items) != 1 {
			t.Fatalf("records=%+v", page.Items)
		}
		return page.Items[0]
	}
	if listed().Stale {
		t.Fatal("a deck made from the current numbers is not out of date")
	}

	// 원본을 고치면 덱은 옛말을 하게 된다.
	current := requestAs[workbook.Workbook](t, server, "alice", http.MethodGet, "/api/v1/workbooks/"+book, nil, http.StatusOK)
	requestAs[map[string]any](t, server, "alice", http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch",
		map[string]any{"base_version": current.Version, "idempotency_key": "moved-on", "cells": []map[string]any{
			{"row": 2, "column": 2, "value": 200},
		}}, http.StatusOK)
	if !listed().Stale {
		t.Fatal("the deck was not reported as out of date after the range changed")
	}

	refreshed := requestAs[struct {
		Presentation presentation.Result `json:"presentation"`
		Record       presentation.Record `json:"record"`
	}](t, server, "alice", http.MethodPost, "/api/v1/presentations/"+created.Presentation.ID+"/refresh", map[string]any{}, http.StatusOK)
	// 같은 덱이어야 한다. 새 덱을 만들면 이미 보낸 링크가 옛 숫자를 계속 보여 준다.
	if refreshed.Presentation.ID != created.Presentation.ID {
		t.Fatalf("refresh made a different deck: %s", refreshed.Presentation.ID)
	}
	if refreshed.Record.Stale {
		t.Fatal("the refreshed deck is still reported as out of date")
	}
	if listed().Stale {
		t.Fatal("the stored record was not brought up to date")
	}
	if len(provider.replaced) != 1 {
		t.Fatalf("the provider was asked to replace %d times", len(provider.replaced))
	}
	// 새로 읽은 숫자가 들어가야 한다.
	if !deckMentions(provider.replaced[0], "200") {
		t.Fatalf("the refreshed deck does not carry the new number: %+v", provider.replaced[0])
	}
}

func TestPresentationRefreshFollowsTheWorkbook(t *testing.T) {
	t.Parallel()
	server, provider, sheetID := presentationServer(t)
	created := requestAs[struct {
		Presentation presentation.Result `json:"presentation"`
	}](t, server, "alice", http.MethodPost, "/api/v1/sheets/"+sheetID+"/presentations",
		map[string]any{"range": "A1:B4"}, http.StatusCreated)
	requestAs[map[string]any](t, server, "mallory", http.MethodPost, "/api/v1/presentations/"+created.Presentation.ID+"/refresh", map[string]any{}, http.StatusForbidden)
	if len(provider.replaced) != 0 {
		t.Fatal("a stranger reached the provider")
	}
}

// deckMentions looks for a value anywhere a slide would print it.
func deckMentions(deck presentation.Deck, needle string) bool {
	for _, slide := range deck.Slides {
		if strings.Contains(slide.Title+" "+slide.Lead, needle) {
			return true
		}
		for _, bullet := range slide.Bullets {
			if strings.Contains(bullet, needle) {
				return true
			}
		}
		if slide.Component == nil {
			continue
		}
		for _, row := range slide.Component.Rows {
			if strings.Contains(row.Label+" "+strings.Join(row.Fields, " "), needle) {
				return true
			}
		}
	}
	return false
}

func doRequest(t *testing.T, server *httptest.Server, actor, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if actor != "" {
		req.Header.Set("X-Kanpic-Actor", actor)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func workbookOf(t *testing.T, server *httptest.Server, sheetID string) string {
	t.Helper()
	listed := requestAs[struct {
		Items []workbook.Workbook `json:"items"`
	}](t, server, "alice", http.MethodGet, "/api/v1/workbooks", nil, http.StatusOK)
	for _, book := range listed.Items {
		full := requestAs[workbook.Workbook](t, server, "alice", http.MethodGet, "/api/v1/workbooks/"+book.ID, nil, http.StatusOK)
		for _, sheet := range full.Sheets {
			if sheet.ID == sheetID {
				return book.ID
			}
		}
	}
	t.Fatalf("no workbook holds sheet %s", sheetID)
	return ""
}
