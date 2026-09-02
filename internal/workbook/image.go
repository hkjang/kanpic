package workbook

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"  // DecodeConfig
	_ "image/jpeg" // DecodeConfig
	_ "image/png"  // DecodeConfig
	"net/http"
	"strings"
	"time"

	"kanpic/pkg/identity"
)

// Image is a picture floating over a sheet. The bytes themselves never travel
// in this record; they are served by their own endpoint so a list of images
// stays small.
type Image struct {
	ID              string        `json:"id"`
	WorkbookID      string        `json:"workbook_id"`
	WorkbookVersion int64         `json:"workbook_version"`
	SheetID         string        `json:"sheet_id"`
	CreateKey       string        `json:"-"`
	ContentType     string        `json:"content_type"`
	ByteSize        int           `json:"byte_size"`
	NaturalWidth    int           `json:"natural_width"`
	NaturalHeight   int           `json:"natural_height"`
	Position        ChartPosition `json:"position"`
	Revision        int64         `json:"revision"`
	CreatedBy       string        `json:"created_by"`
	UpdatedBy       string        `json:"updated_by"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	data            []byte
}

// Bytes returns the stored picture. It is only populated by GetImageContent.
func (i Image) Bytes() []byte { return i.data }

type CreateImageInput struct {
	IdempotencyKey string
	SheetID        string
	Data           []byte
	MaxBytes       int
	Position       *ChartPosition
}

type UpdateImageInput struct {
	Position         *ChartPosition `json:"position,omitempty"`
	ExpectedRevision *int64         `json:"expected_revision,omitempty"`
}

const (
	MaxImagesPerWorkbook = 50
	DefaultMaxImageBytes = 5 << 20
	maxImageSide         = 4000
	minImageSide         = 16
	// defaultImageWidth bounds how large a picture lands on the sheet; a
	// 4000-pixel photograph should not cover the whole grid on arrival.
	defaultImageWidth = 360
)

// sniffImage identifies the picture from its bytes, never from a declared
// type, and reports its size. Anything that is not a picture is refused —
// a browser served a mislabelled SVG would run it.
func sniffImage(data []byte) (contentType string, width, height int, err error) {
	if len(data) == 0 {
		return "", 0, 0, fmt.Errorf("%w: image is empty", ErrInvalid)
	}
	contentType = http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg", "image/gif":
		config, _, decodeErr := image.DecodeConfig(bytes.NewReader(data))
		if decodeErr != nil {
			return "", 0, 0, fmt.Errorf("%w: image could not be read", ErrInvalid)
		}
		width, height = config.Width, config.Height
	case "image/webp":
		width, height, err = webpSize(data)
		if err != nil {
			return "", 0, 0, err
		}
	default:
		return "", 0, 0, fmt.Errorf("%w: only PNG, JPEG, GIF and WebP pictures can be inserted", ErrInvalid)
	}
	if width < 1 || height < 1 || width > 20_000 || height > 20_000 {
		return "", 0, 0, fmt.Errorf("%w: image dimensions are out of range", ErrInvalid)
	}
	return contentType, width, height, nil
}

// webpSize reads the dimensions from the RIFF container without a decoder:
// VP8X carries them in its canvas fields, VP8L packs them into 28 bits, and a
// lossy VP8 frame keeps them after the start code.
func webpSize(data []byte) (int, int, error) {
	bad := fmt.Errorf("%w: image could not be read", ErrInvalid)
	if len(data) < 30 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, bad
	}
	chunk := string(data[12:16])
	switch chunk {
	case "VP8X":
		width := int(data[24]) | int(data[25])<<8 | int(data[26])<<16
		height := int(data[27]) | int(data[28])<<8 | int(data[29])<<16
		return width + 1, height + 1, nil
	case "VP8L":
		if len(data) < 25 || data[20] != 0x2f {
			return 0, 0, bad
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
	case "VP8 ":
		if len(data) < 30 || data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, bad
		}
		return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff), nil
	}
	return 0, 0, bad
}

// ImageDimensions reports a picture's natural size, refusing what is not a
// picture. Import uses it to turn a file's scale factors into a display size.
func ImageDimensions(data []byte) (width, height int, err error) {
	_, width, height, err = sniffImage(data)
	return width, height, err
}

func imageFromInput(workbookID, key, actor string, input CreateImageInput) (Image, error) {
	sheetID := strings.TrimSpace(input.SheetID)
	if sheetID == "" {
		return Image{}, fmt.Errorf("%w: sheet_id is required", ErrInvalid)
	}
	limit := input.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxImageBytes
	}
	if len(input.Data) > limit {
		return Image{}, fmt.Errorf("%w: image exceeds %d MB", ErrInvalid, limit>>20)
	}
	contentType, width, height, err := sniffImage(input.Data)
	if err != nil {
		return Image{}, err
	}
	position := ChartPosition{X: 24, Y: 24}
	if input.Position != nil {
		position = *input.Position
	}
	if position.Width <= 0 || position.Height <= 0 {
		// Fit to the default width and keep the picture's shape. A picture
		// smaller than the smallest thing a hand can grab is scaled up to it —
		// a 1×1 pixel is a legitimate picture, not a mistake to refuse.
		scale := 1.0
		if width > defaultImageWidth {
			scale = float64(defaultImageWidth) / float64(width)
		}
		if lift := float64(minImageSide) / (float64(min(width, height)) * scale); lift > 1 {
			scale *= lift
		}
		position.Width = max(minImageSide, int(float64(width)*scale+0.5))
		position.Height = max(minImageSide, int(float64(height)*scale+0.5))
	}
	if err := validateImagePosition(&position); err != nil {
		return Image{}, err
	}
	return Image{WorkbookID: workbookID, SheetID: sheetID, CreateKey: key, ContentType: contentType, ByteSize: len(input.Data),
		NaturalWidth: width, NaturalHeight: height, Position: position, CreatedBy: actor, UpdatedBy: actor, data: input.Data}, nil
}

func validateImagePosition(position *ChartPosition) error {
	if position.X < 0 || position.Y < 0 {
		return fmt.Errorf("%w: image position must not be negative", ErrInvalid)
	}
	if position.Width < minImageSide || position.Height < minImageSide || position.Width > maxImageSide || position.Height > maxImageSide {
		return fmt.Errorf("%w: image size must be between %d and %d pixels", ErrInvalid, minImageSide, maxImageSide)
	}
	return nil
}

func cloneImage(item Image) Image {
	item.data = nil
	return item
}

// --- memory repository ---

func (r *MemoryRepository) imagesForWorkbookLocked(workbookID, sheetID string) []Image {
	items := make([]Image, 0)
	for _, item := range r.images {
		if item.WorkbookID == workbookID && (sheetID == "" || item.SheetID == sheetID) {
			items = append(items, cloneImage(item))
		}
	}
	sortImages(items)
	return items
}

func sortImages(items []Image) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && (items[j].CreatedAt.Before(items[j-1].CreatedAt) || (items[j].CreatedAt.Equal(items[j-1].CreatedAt) && items[j].ID < items[j-1].ID)); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func (r *MemoryRepository) CreateImage(_ context.Context, workbookID, actor string, input CreateImageInput) (Image, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return Image{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, found := r.workbooks[workbookID]
	if !found || state.deletedAt != nil {
		return Image{}, ErrNotFound
	}
	for _, item := range r.images {
		if item.WorkbookID == workbookID && item.CreatedBy == actor && item.CreateKey == key {
			item.WorkbookVersion = state.workbook.Version
			return cloneImage(item), nil
		}
	}
	if len(r.imagesForWorkbookLocked(workbookID, "")) >= MaxImagesPerWorkbook {
		return Image{}, fmt.Errorf("%w: a workbook may contain at most %d images", ErrInvalid, MaxImagesPerWorkbook)
	}
	item, err := imageFromInput(workbookID, key, actor, input)
	if err != nil {
		return Image{}, err
	}
	if sheet, ok := state.sheets[item.SheetID]; !ok || sheet.WorkbookID != workbookID {
		return Image{}, fmt.Errorf("%w: sheet does not belong to the workbook", ErrInvalid)
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	r.bump(state)
	item.WorkbookVersion = state.workbook.Version
	r.images[item.ID] = item
	return cloneImage(item), nil
}

func (r *MemoryRepository) ListImages(_ context.Context, workbookID, sheetID string) ([]Image, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, found := r.workbooks[workbookID]
	if !found || state.deletedAt != nil {
		return nil, ErrNotFound
	}
	items := r.imagesForWorkbookLocked(workbookID, strings.TrimSpace(sheetID))
	for index := range items {
		items[index].WorkbookVersion = state.workbook.Version
	}
	return items, nil
}

func (r *MemoryRepository) GetImage(_ context.Context, imageID string) (Image, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.images[imageID]
	if !ok {
		return Image{}, ErrNotFound
	}
	if state, found := r.workbooks[item.WorkbookID]; found {
		item.WorkbookVersion = state.workbook.Version
	}
	return cloneImage(item), nil
}

func (r *MemoryRepository) GetImageContent(_ context.Context, imageID string) (Image, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.images[imageID]
	if !ok {
		return Image{}, ErrNotFound
	}
	copied := item
	copied.data = append([]byte(nil), item.data...)
	return copied, nil
}

func (r *MemoryRepository) UpdateImage(_ context.Context, imageID, actor string, input UpdateImageInput) (Image, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.images[imageID]
	if !ok {
		return Image{}, ErrNotFound
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != item.Revision {
		return Image{}, ErrRevision
	}
	if input.Position != nil {
		position := *input.Position
		if err := validateImagePosition(&position); err != nil {
			return Image{}, err
		}
		item.Position = position
	}
	state := r.workbooks[item.WorkbookID]
	item.Revision++
	item.UpdatedBy, item.UpdatedAt = actor, r.now()
	r.bump(state)
	item.WorkbookVersion = state.workbook.Version
	r.images[imageID] = item
	return cloneImage(item), nil
}

func (r *MemoryRepository) DeleteImage(_ context.Context, imageID, actor string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.images[imageID]
	if !ok {
		return ErrNotFound
	}
	if expectedRevision != nil && *expectedRevision != item.Revision {
		return ErrRevision
	}
	_ = actor
	delete(r.images, imageID)
	if state, found := r.workbooks[item.WorkbookID]; found {
		r.bump(state)
	}
	return nil
}
