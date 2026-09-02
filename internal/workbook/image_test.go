package workbook

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		picture.Set(x, 0, color.RGBA{R: 200, A: 255})
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// 최소한의 WebP(VP8L) 머리. 디코더 없이 크기만 읽는 것을 확인한다.
func webpLosslessHeader(width, height int) []byte {
	out := []byte("RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2f")
	bits := uint32(width-1) | uint32(height-1)<<14
	var packed [4]byte
	binary.LittleEndian.PutUint32(packed[:], bits)
	out = append(out, packed[:]...)
	return append(out, make([]byte, 8)...)
}

// 그림의 종류는 파일 이름이 아니라 바이트에서 읽는다. 잘못 붙은 SVG 를 브라우저는
// 실행한다.
func TestImagesAreIdentifiedFromTheirBytes(t *testing.T) {
	t.Parallel()
	if kind, width, height, err := sniffImage(pngBytes(t, 40, 30)); err != nil || kind != "image/png" || width != 40 || height != 30 {
		t.Fatalf("png: %s %dx%d %v", kind, width, height, err)
	}
	if kind, width, height, err := sniffImage(webpLosslessHeader(300, 200)); err != nil || kind != "image/webp" || width != 300 || height != 200 {
		t.Fatalf("webp: %s %dx%d %v", kind, width, height, err)
	}
	for name, data := range map[string][]byte{
		"svg":   []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":  []byte(`<!doctype html><html><body>x</body></html>`),
		"text":  []byte("hello"),
		"empty": {},
	} {
		if _, _, _, err := sniffImage(data); err == nil {
			t.Errorf("%s 는 거절해야 한다", name)
		}
	}
}

func TestImageLifecycleOnTheMemoryRepository(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "그림", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	created, err := repository.CreateImage(ctx, book.ID, "alice", CreateImageInput{IdempotencyKey: "img-1", SheetID: sheet, Data: pngBytes(t, 800, 400)})
	if err != nil {
		t.Fatal(err)
	}
	// 큰 사진은 기본 너비에 맞춰 놓되 비율을 지킨다.
	if created.Position.Width != 360 || created.Position.Height != 180 || created.NaturalWidth != 800 {
		t.Fatalf("position = %+v natural %dx%d", created.Position, created.NaturalWidth, created.NaturalHeight)
	}
	if created.WorkbookVersion != book.Version+1 {
		t.Fatalf("version %d, want %d", created.WorkbookVersion, book.Version+1)
	}
	again, err := repository.CreateImage(ctx, book.ID, "alice", CreateImageInput{IdempotencyKey: "img-1", SheetID: sheet, Data: pngBytes(t, 800, 400)})
	if err != nil || again.ID != created.ID {
		t.Fatalf("같은 멱등 키는 같은 그림이어야 한다: %v %s", err, again.ID)
	}
	content, err := repository.GetImageContent(ctx, created.ID)
	if err != nil || len(content.Bytes()) != created.ByteSize || content.ContentType != "image/png" {
		t.Fatalf("content: %v %d", err, len(content.Bytes()))
	}
	listed, err := repository.ListImages(ctx, book.ID, sheet)
	if err != nil || len(listed) != 1 || len(listed[0].Bytes()) != 0 {
		t.Fatalf("목록에는 바이트가 실리지 않아야 한다: %v %d", err, len(listed))
	}
	stale := int64(99)
	if _, err := repository.UpdateImage(ctx, created.ID, "alice", UpdateImageInput{Position: &ChartPosition{X: 10, Y: 20, Width: 100, Height: 50}, ExpectedRevision: &stale}); err != ErrRevision {
		t.Fatalf("stale revision: %v", err)
	}
	moved, err := repository.UpdateImage(ctx, created.ID, "alice", UpdateImageInput{Position: &ChartPosition{X: 10, Y: 20, Width: 100, Height: 50}, ExpectedRevision: &created.Revision})
	if err != nil || moved.Position.X != 10 || moved.Revision != 2 {
		t.Fatalf("move: %v %+v", err, moved.Position)
	}
	if _, err := repository.UpdateImage(ctx, created.ID, "alice", UpdateImageInput{Position: &ChartPosition{X: -1, Y: 0, Width: 100, Height: 50}}); err == nil {
		t.Fatal("음수 위치는 거절해야 한다")
	}
	if err := repository.DeleteImage(ctx, created.ID, "alice", &created.Revision); err != ErrRevision {
		t.Fatalf("stale delete: %v", err)
	}
	if err := repository.DeleteImage(ctx, created.ID, "alice", &moved.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetImage(ctx, created.ID); err != ErrNotFound {
		t.Fatalf("deleted image: %v", err)
	}
}

func TestImageCeilings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "한도", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	if _, err := repository.CreateImage(ctx, book.ID, "alice", CreateImageInput{IdempotencyKey: "big", SheetID: sheet, Data: pngBytes(t, 40, 30), MaxBytes: 10}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("size cap: %v", err)
	}
	other, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "남", OwnerID: "bob"})
	if _, err := repository.CreateImage(ctx, book.ID, "alice", CreateImageInput{IdempotencyKey: "wrong-sheet", SheetID: other.Sheets[0].ID, Data: pngBytes(t, 40, 30)}); err == nil {
		t.Fatal("다른 워크북의 시트에는 넣을 수 없어야 한다")
	}
	for index := 0; index < MaxImagesPerWorkbook; index++ {
		if _, err := repository.CreateImage(ctx, book.ID, "alice", CreateImageInput{IdempotencyKey: "n" + strings.Repeat("x", index), SheetID: sheet, Data: pngBytes(t, 20, 20)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.CreateImage(ctx, book.ID, "alice", CreateImageInput{IdempotencyKey: "one-too-many", SheetID: sheet, Data: pngBytes(t, 20, 20)}); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("count cap: %v", err)
	}
}
