package importexport

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"kanpic/internal/workbook"
)

func roundTripPNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 120, 60))
	for x := 0; x < 120; x++ {
		picture.Set(x, 30, color.RGBA{G: 180, A: 255})
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// 몇 분 전에 여기서 내보낸 워크북은 그림까지 그대로 돌아와야 한다. 자리는 셀 닻과
// 배율로 근사하고, 바이트는 한 자도 바뀌지 않는다.
func TestPicturesSurviveTheXLSXRoundTrip(t *testing.T) {
	ctx := context.Background()
	repository := workbook.NewMemoryRepository()
	service := New(repository)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "그림 왕복", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	original := roundTripPNG(t)
	placed := workbook.ChartPosition{X: 250, Y: 70, Width: 240, Height: 120}
	if _, err := repository.CreateImage(ctx, book.ID, "alice", workbook.CreateImageInput{IdempotencyKey: "p1", SheetID: book.Sheets[0].ID, Data: original, Position: &placed}); err != nil {
		t.Fatal(err)
	}
	exported, err := service.Export(ctx, ExportRequest{WorkbookID: book.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	if exported.SkippedImages != 0 {
		t.Fatalf("export skipped %d pictures", exported.SkippedImages)
	}
	parsed, err := parseXLSX("왕복.xlsx", "왕복", exported.Data, 50<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sheets) == 0 || len(parsed.Sheets[0].Images) != 1 {
		t.Fatalf("parsed images = %d", len(parsed.Sheets[0].Images))
	}
	if !bytes.Equal(parsed.Sheets[0].Images[0].Data, original) {
		t.Fatal("picture bytes changed on the way through the file")
	}
	// 250px 는 세 번째 열(108px 씩) 안 34px 지점, 70px 는 세 번째 행(27px 씩) 안 16px 지점이다.
	got := parsed.Sheets[0].Images[0].Position
	if got.X != 250 || got.Y != 70 || got.Width != 240 || got.Height != 120 {
		t.Fatalf("position after round trip = %+v, want %+v", got, placed)
	}

	imported, err := service.Import(ctx, ImportRequest{FileName: "왕복.xlsx", Data: exported.Data, ActorID: "alice", IdempotencyKey: "import-1"})
	if err != nil {
		t.Fatal(err)
	}
	images, err := repository.ListImages(ctx, imported.ID, "")
	if err != nil || len(images) != 1 {
		t.Fatalf("imported images = %d (%v)", len(images), err)
	}
	if images[0].NaturalWidth != 120 || images[0].NaturalHeight != 60 || images[0].ContentType != "image/png" {
		t.Fatalf("imported picture = %+v", images[0])
	}
	content, err := repository.GetImageContent(ctx, images[0].ID)
	if err != nil || !bytes.Equal(content.Bytes(), original) {
		t.Fatal("imported bytes differ from the original")
	}
}
