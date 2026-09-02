package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"kanpic/internal/workbook"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 8, 4))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func uploadImage(t *testing.T, server *httptest.Server, actor, sheetID, key string, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "picture.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = writer.Close()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/sheets/"+sheetID+"/images", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Idempotency-Key", key)
	if actor != "" {
		req.Header.Set("X-Kanpic-Actor", actor)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func fetch(t *testing.T, server *httptest.Server, actor, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
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

// 그림은 사진일 수 있다. 워크북을 볼 수 없는 사람은 그 본체도 볼 수 없어야 하고,
// 본체는 올릴 때 읽은 종류 그대로, 브라우저가 다시 추측하지 않게 나가야 한다.
func TestImageUploadServeAndAccess(t *testing.T) {
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	book := requestAs[workbook.Workbook](t, server, "alice", http.MethodPost, "/api/v1/workbooks", map[string]any{"title": "사진"}, http.StatusCreated)
	sheet := book.Sheets[0].ID

	created := uploadImage(t, server, "alice", sheet, "img-1", testPNG(t))
	if created.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(created.Body)
		t.Fatalf("upload: %d %s", created.StatusCode, data)
	}
	var item workbook.Image
	decodeBody(t, created, &item)
	if item.ContentType != "image/png" || item.NaturalWidth != 8 || item.NaturalHeight != 4 {
		t.Fatalf("item = %+v", item)
	}

	content := fetch(t, server, "alice", "/api/v1/images/"+item.ID+"/content")
	if content.StatusCode != http.StatusOK || content.Header.Get("Content-Type") != "image/png" || content.Header.Get("X-Content-Type-Options") != "nosniff" || content.Header.Get("Content-Disposition") != "inline" {
		t.Fatalf("content: %d %v", content.StatusCode, content.Header)
	}
	served, _ := io.ReadAll(content.Body)
	if !bytes.Equal(served, testPNG(t)) {
		t.Fatal("served bytes differ from what was uploaded")
	}

	// 공유받지 않은 다른 사용자.
	if stranger := fetch(t, server, "mallory", "/api/v1/images/"+item.ID+"/content"); stranger.StatusCode == http.StatusOK {
		t.Fatal("a user without access read the picture")
	}
	if stranger := fetch(t, server, "mallory", "/api/v1/workbooks/"+book.ID+"/images"); stranger.StatusCode == http.StatusOK {
		t.Fatal("a user without access listed the pictures")
	}
	// 그림이 아닌 것은 이름이 무엇이든 거절한다.
	if refused := uploadImage(t, server, "alice", sheet, "svg-1", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)); refused.StatusCode != http.StatusBadRequest {
		t.Fatalf("svg accepted: %d", refused.StatusCode)
	}
}

func decodeBody(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
}
