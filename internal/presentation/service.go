package presentation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

// ErrInvalid marks a request kanpic can see is wrong before asking anybody.
var ErrInvalid = errors.New("invalid presentation request")

type settingsProvider interface {
	Values(context.Context) (map[string]any, error)
}

// Config is what an administrator set up. It is read on every request rather
// than at startup so turning the feature on does not need a restart.
type Config struct {
	Enabled    bool          `json:"enabled"`
	Provider   string        `json:"provider"`
	BaseURL    string        `json:"base_url"`
	APIKey     string        `json:"-"`
	Timeout    time.Duration `json:"-"`
	TemplateID string        `json:"default_template_id"`
	MaxCells   int           `json:"max_cells"`
}

// Factory builds a provider from the stored configuration. Registering one is
// how a presentation product is added; nothing else in kanpic changes.
type Factory func(Config) (Provider, error)

type Service struct {
	settings  settingsProvider
	workbooks workbook.Repository
	store     Store
	factories map[string]Factory
}

func NewService(settings settingsProvider, workbooks workbook.Repository, store Store, factories map[string]Factory) *Service {
	if factories == nil {
		factories = map[string]Factory{}
	}
	if store == nil {
		store = NewMemoryStore()
	}
	return &Service{settings: settings, workbooks: workbooks, store: store, factories: factories}
}

func (s *Service) Config(ctx context.Context) (Config, error) {
	values, err := s.settings.Values(ctx)
	if err != nil {
		return Config{}, err
	}
	config := Config{
		Enabled:    boolSetting(values["presentation.enabled"]),
		Provider:   strings.TrimSpace(stringSetting(values["presentation.provider"], "ptium")),
		BaseURL:    strings.TrimSpace(stringSetting(values["presentation.base_url"], "")),
		APIKey:     strings.TrimSpace(stringSetting(values["presentation.api_key"], "")),
		Timeout:    time.Duration(numberSetting(values["presentation.timeout_seconds"], 60)) * time.Second,
		TemplateID: strings.TrimSpace(stringSetting(values["presentation.default_template_id"], "")),
		MaxCells:   int(numberSetting(values["presentation.max_cells"], 5000)),
	}
	if config.MaxCells <= 0 || config.MaxCells > MaxAnalysisCells {
		config.MaxCells = MaxAnalysisCells
	}
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	return config, nil
}

// Available reports whether a person should be offered the button at all.
func (s *Service) Available(ctx context.Context) (Config, bool) {
	config, err := s.Config(ctx)
	if err != nil || !config.Enabled || config.BaseURL == "" {
		return config, false
	}
	_, known := s.factories[config.Provider]
	return config, known
}

func (s *Service) provider(ctx context.Context) (Provider, Config, error) {
	config, err := s.Config(ctx)
	if err != nil {
		return nil, config, err
	}
	if !config.Enabled || config.BaseURL == "" {
		return nil, config, ErrNotConfigured
	}
	factory, known := s.factories[config.Provider]
	if !known {
		return nil, config, fmt.Errorf("%w: %q", ErrNotConfigured, config.Provider)
	}
	provider, err := factory(config)
	if err != nil {
		return nil, config, err
	}
	return provider, config, nil
}

func (s *Service) Templates(ctx context.Context) ([]Template, error) {
	provider, _, err := s.provider(ctx)
	if err != nil {
		return nil, err
	}
	return provider.Templates(ctx)
}

// CreateRequestInput is one press of the button.
type CreateRequestInput struct {
	ActorID      string
	SheetID      string
	Range        string
	Title        string
	Language     string
	TemplateID   string
	IncludeTable bool
}

// Create reads the range, works out what it means, and hands the deck to the
// provider. The analysis happens here rather than in the provider because it is
// the spreadsheet's own knowledge of its numbers, and it must come out the same
// whichever presentation product is on the other end.
func (s *Service) Create(ctx context.Context, input CreateRequestInput) (Result, Analysis, error) {
	provider, config, err := s.provider(ctx)
	if err != nil {
		return Result{}, Analysis{}, err
	}
	analysis, err := s.analyze(ctx, config, input.SheetID, input.Range)
	if err != nil {
		return Result{}, Analysis{}, err
	}
	deck := Build(analysis, DeckOptions{Title: input.Title, Language: input.Language, IncludeTable: input.IncludeTable})
	result, err := provider.Create(ctx, CreateRequest{Deck: deck, TemplateID: input.TemplateID})
	if err != nil {
		return Result{}, analysis, err
	}
	result.Source = analysis.Source
	// 기록이 남아야 이 덱을 누가 볼 수 있는지 판단할 수 있다. 기록에 실패하면
	// 덱은 이미 만들어졌지만 kanpic 은 그것을 자기 것으로 인정하지 않는다 —
	// 만든 사람에게는 그렇게 말해야 한다.
	record := Record{
		ID: result.ID, Provider: config.Provider, WorkbookID: analysis.Source.WorkbookID,
		SheetID: analysis.Source.SheetID, Range: analysis.Source.Range, SourceVersion: analysis.Source.Version,
		Title: result.Title, Template: result.Template, SlideCount: result.SlideCount, EditURL: result.EditURL,
		CreatedBy: input.ActorID, CreatedAt: time.Now().UTC(),
	}
	if err := s.store.Save(ctx, record); err != nil {
		return Result{}, analysis, err
	}
	return result, analysis, nil
}

// Record returns kanpic's own note of a deck. The authorization layer asks for
// it before letting anybody near the provider.
func (s *Service) Record(ctx context.Context, id string) (Record, error) {
	record, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return Record{}, err
	}
	return s.withStaleness(ctx, record), nil
}

// WorkbookIDFor is what the authorization middleware calls. A deck kanpic has
// no record of belongs to nobody, and is refused.
func (s *Service) WorkbookIDFor(ctx context.Context, id string) (string, error) {
	record, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return "", err
	}
	return record.WorkbookID, nil
}

func (s *Service) ListForWorkbook(ctx context.Context, workbookID string) ([]Record, error) {
	records, err := s.store.ListForWorkbook(ctx, workbookID)
	if err != nil {
		return nil, err
	}
	for index := range records {
		records[index] = s.withStaleness(ctx, records[index])
	}
	return records, nil
}

// withStaleness answers the question the panel exists to ask: has the table
// this deck was made from changed since? The workbook version is a single
// counter, so a deck is only ever reported as possibly out of date, never as
// definitely wrong — something elsewhere in the workbook may have moved.
func (s *Service) withStaleness(ctx context.Context, record Record) Record {
	if record.SourceVersion <= 0 || record.WorkbookID == "" {
		return record
	}
	book, err := s.workbooks.GetWorkbook(ctx, record.WorkbookID)
	if err != nil {
		return record
	}
	record.Stale = book.Version > record.SourceVersion
	return record
}

// Preview runs everything except the call to the provider. It is what the
// dialog shows before anybody commits: the same slides, from the same code, so
// what is previewed is what is made.
func (s *Service) Preview(ctx context.Context, input CreateRequestInput) (Deck, Analysis, error) {
	config, err := s.Config(ctx)
	if err != nil {
		return Deck{}, Analysis{}, err
	}
	analysis, err := s.analyze(ctx, config, input.SheetID, input.Range)
	if err != nil {
		return Deck{}, Analysis{}, err
	}
	return Build(analysis, DeckOptions{Title: input.Title, Language: input.Language, IncludeTable: input.IncludeTable}), analysis, nil
}

func (s *Service) Export(ctx context.Context, id, format string) ([]byte, string, string, error) {
	provider, _, err := s.provider(ctx)
	if err != nil {
		return nil, "", "", err
	}
	if strings.TrimSpace(id) == "" {
		return nil, "", "", fmt.Errorf("%w: presentation id is required", ErrInvalid)
	}
	return provider.Export(ctx, id, format)
}

func (s *Service) analyze(ctx context.Context, config Config, sheetID, address string) (Analysis, error) {
	selected, err := cellrange.Parse(strings.TrimSpace(address))
	if err != nil {
		return Analysis{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	rows := selected.End.Row - selected.Start.Row + 1
	columns := selected.End.Column - selected.Start.Column + 1
	if rows*columns > config.MaxCells {
		return Analysis{}, fmt.Errorf("%w: a presentation reads at most %d cells", ErrInvalid, config.MaxCells)
	}
	cells, err := s.workbooks.ReadRange(ctx, sheetID, selected)
	if err != nil {
		return Analysis{}, err
	}
	// 출처는 덱의 부속물이 아니라 덱이 하는 말의 근거다. 알아내지 못하면
	// 슬라이드가 어느 표에서 왔는지 말할 수 없으므로, 조용히 비워 두지 않고
	// 실패로 돌려준다.
	sheet, err := s.sheetContext(ctx, sheetID)
	if err != nil {
		return Analysis{}, err
	}
	source := SourceRef{
		WorkbookID: sheet.workbookID, SheetID: sheetID, SheetName: sheet.name,
		Range: normalizedRange(selected), Version: sheet.version, CapturedAt: time.Now().UTC(),
	}
	return Analyze(source, selected, cells), nil
}

type sheetContext struct {
	workbookID string
	name       string
	version    int64
}

// sheetContext puts a workbook, a name and a version on the deck's source.
func (s *Service) sheetContext(ctx context.Context, sheetID string) (sheetContext, error) {
	workbookID, err := s.workbooks.WorkbookIDForResource(ctx, "sheetId", sheetID)
	if err != nil {
		return sheetContext{}, err
	}
	book, err := s.workbooks.GetWorkbook(ctx, workbookID)
	if err != nil {
		return sheetContext{}, err
	}
	context := sheetContext{workbookID: book.ID, version: book.Version}
	for _, sheet := range book.Sheets {
		if sheet.ID == sheetID {
			context.name = sheet.Name
			break
		}
	}
	return context, nil
}

func normalizedRange(selected cellrange.Range) string {
	return cellrange.Address(selected.Start.Row, selected.Start.Column) + ":" + cellrange.Address(selected.End.Row, selected.End.Column)
}

func boolSetting(value any) bool {
	flag, ok := value.(bool)
	return ok && flag
}

func stringSetting(value any, fallback string) string {
	text, ok := value.(string)
	if !ok {
		return fallback
	}
	return text
}

func numberSetting(value any, fallback float64) float64 {
	number, ok := value.(float64)
	if !ok {
		return fallback
	}
	return number
}
