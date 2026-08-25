package importexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/formula"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

const (
	DefaultMaxUploadBytes   = 20 << 20
	DefaultMaxExpandedBytes = 200 << 20
	MaxImportCells          = 1_000_000
	// Excel caps a comment at 32,512 characters and its author at 255. A note
	// longer than that would make the whole export fail, so it is cut instead.
	maxCommentLength    = 32_512
	exportCommentAuthor = "kanpic"
)

// trimComment keeps a note within what a comment may hold, cutting on a rune
// boundary so a multi-byte character never ends up halved.
func trimComment(note string) string {
	note = strings.TrimSpace(note)
	if len(note) <= maxCommentLength {
		return note
	}
	cut := maxCommentLength
	for cut > 0 && !utf8.RuneStart(note[cut]) {
		cut--
	}
	return note[:cut]
}

type SheetPreview struct {
	Name          string `json:"name"`
	Rows          int    `json:"rows"`
	Columns       int    `json:"columns"`
	NonEmptyCells int    `json:"non_empty_cells"`
}

type Preview struct {
	FileName   string         `json:"file_name"`
	Format     string         `json:"format"`
	SizeBytes  int            `json:"size_bytes"`
	Sheets     []SheetPreview `json:"sheets"`
	TotalCells int            `json:"total_cells"`
	Warnings   []string       `json:"warnings"`
}

type ParsedWorkbook struct {
	Title          string
	Format         string
	Sheets         []workbook.ImportSheet
	NamedRanges    []workbook.ImportNamedRange
	NamedFunctions []workbook.ImportNamedFunction
	Preview        Preview
}

type ImportRequest struct {
	FileName         string
	Data             []byte
	WorkspaceID      string
	ActorID          string
	IdempotencyKey   string
	MaxExpandedBytes int64
}

type ExportRequest struct {
	WorkbookID string `json:"workbook_id"`
	SheetID    string `json:"sheet_id,omitempty"`
	Format     string `json:"format"`
}

type ExportedFile struct {
	Name        string
	ContentType string
	Data        []byte
}

type Service struct{ repository workbook.Repository }

func New(repository workbook.Repository) *Service { return &Service{repository: repository} }

func (s *Service) Preview(_ context.Context, fileName string, data []byte, maxExpanded int64) (Preview, error) {
	parsed, err := Parse(fileName, data, maxExpanded)
	if err != nil {
		return Preview{}, err
	}
	return parsed.Preview, nil
}

func (s *Service) Import(ctx context.Context, request ImportRequest) (workbook.Workbook, error) {
	parsed, err := Parse(request.FileName, request.Data, request.MaxExpandedBytes)
	if err != nil {
		return workbook.Workbook{}, err
	}
	return s.repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{WorkspaceID: request.WorkspaceID, Title: parsed.Title, OwnerID: request.ActorID, ActorID: request.ActorID, IdempotencyKey: request.IdempotencyKey, FileName: request.FileName, Format: parsed.Format, Sheets: parsed.Sheets, NamedRanges: parsed.NamedRanges, NamedFunctions: parsed.NamedFunctions})
}

func Parse(fileName string, data []byte, maxExpanded int64) (ParsedWorkbook, error) {
	if len(data) == 0 {
		return ParsedWorkbook{}, errors.New("file is empty")
	}
	if !utf8.ValidString(fileName) {
		return ParsedWorkbook{}, errors.New("file name is not valid UTF-8")
	}
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	title := strings.TrimSpace(strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName)))
	if title == "" {
		title = "가져온 워크북"
	}
	switch format {
	case "csv", "tsv":
		return parseDelimited(fileName, title, format, data)
	case "xlsx":
		if maxExpanded <= 0 {
			maxExpanded = DefaultMaxExpandedBytes
		}
		return parseXLSX(fileName, title, data, maxExpanded)
	case "xlsm":
		return ParsedWorkbook{}, errors.New("macro-enabled workbooks are not accepted; save as .xlsx first")
	default:
		return ParsedWorkbook{}, fmt.Errorf("unsupported import format %q", format)
	}
}

func parseDelimited(fileName, title, format string, data []byte) (ParsedWorkbook, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(data) {
		return ParsedWorkbook{}, errors.New("CSV must be UTF-8 encoded")
	}
	delimiter := '\t'
	if format == "csv" {
		delimiter = detectDelimiter(data)
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = false
	cells := make([]workbook.CellInput, 0)
	rows, maxColumns := 0, 0
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ParsedWorkbook{}, fmt.Errorf("parse delimited row %d: %w", rows+1, err)
		}
		rows++
		if len(record) > maxColumns {
			maxColumns = len(record)
		}
		for column, raw := range record {
			if raw == "" {
				continue
			}
			value, err := json.Marshal(parseScalar(raw))
			if err != nil {
				return ParsedWorkbook{}, err
			}
			cells = append(cells, workbook.CellInput{Row: rows, Column: column + 1, Value: value})
			if len(cells) > MaxImportCells {
				return ParsedWorkbook{}, errors.New("import exceeds one million non-empty cells")
			}
		}
	}
	name := "Sheet1"
	preview := Preview{FileName: fileName, Format: format, SizeBytes: len(data), Sheets: []SheetPreview{{Name: name, Rows: rows, Columns: maxColumns, NonEmptyCells: len(cells)}}, TotalCells: len(cells), Warnings: []string{}}
	return ParsedWorkbook{Title: title, Format: format, Sheets: []workbook.ImportSheet{{Name: name, Cells: cells}}, Preview: preview}, nil
}

func parseXLSX(fileName, title string, data []byte, maxExpanded int64) (ParsedWorkbook, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ParsedWorkbook{}, fmt.Errorf("invalid XLSX archive: %w", err)
	}
	var expanded uint64
	for _, entry := range archive.File {
		if entry.Flags&1 != 0 {
			return ParsedWorkbook{}, errors.New("encrypted XLSX files are not supported")
		}
		expanded += entry.UncompressedSize64
		if expanded > uint64(maxExpanded) {
			return ParsedWorkbook{}, fmt.Errorf("XLSX expands beyond the %d byte safety limit", maxExpanded)
		}
	}
	file, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{RawCellValue: true})
	if err != nil {
		return ParsedWorkbook{}, fmt.Errorf("open XLSX: %w", err)
	}
	defer file.Close()
	sheetNames := file.GetSheetList()
	if len(sheetNames) == 0 {
		return ParsedWorkbook{}, errors.New("XLSX contains no worksheets")
	}
	parsed := ParsedWorkbook{Title: title, Format: "xlsx", Sheets: make([]workbook.ImportSheet, 0, len(sheetNames)), Preview: Preview{FileName: fileName, Format: "xlsx", SizeBytes: len(data), Warnings: []string{}}}
	styleCache := make(map[int]json.RawMessage)
	for _, sheetName := range sheetNames {
		rows, err := file.Rows(sheetName)
		if err != nil {
			return ParsedWorkbook{}, fmt.Errorf("read sheet %s: %w", sheetName, err)
		}
		imported := workbook.ImportSheet{Name: sheetName, Cells: make([]workbook.CellInput, 0)}
		rowIndex, maxColumns := 0, 0
		for rows.Next() {
			rowIndex++
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				return ParsedWorkbook{}, err
			}
			if len(columns) > maxColumns {
				maxColumns = len(columns)
			}
			for columnIndex, displayValue := range columns {
				coordinate, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex)
				// 엑셀은 최신 함수를 _xlfn. 이 붙은 이름으로 적는다. 떼지
				// 않고 저장하면 수식 입력줄에 사람이 쓰지 않은 이름이 뜬다.
				cellFormula, _ := file.GetCellFormula(sheetName, coordinate)
				cellFormula = formula.FromExcel(cellFormula)
				if displayValue == "" && cellFormula == "" {
					continue
				}
				cellType, _ := file.GetCellType(sheetName, coordinate)
				// The row reader hands back the formatted text, which is what a
				// person sees but not what the cell holds: 18000 formatted as
				// currency arrives as "18,000원" and would be stored as words.
				raw, rawErr := file.GetCellValue(sheetName, coordinate, excelize.Options{RawCellValue: true})
				if rawErr != nil {
					raw = displayValue
				}
				value := parseXLSXValue(raw, cellType)
				if text, isText := value.(string); isText && text != displayValue && cellType != excelize.CellTypeNumber {
					// Nothing numeric was recognised, so keep what the file
					// showed rather than the storage form.
					value = displayValue
				}
				encoded, _ := json.Marshal(value)
				input := workbook.CellInput{Row: rowIndex, Column: columnIndex + 1, Value: encoded, Formula: cellFormula}
				styleID, _ := file.GetCellStyle(sheetName, coordinate)
				if styleID > 0 {
					style, ok := styleCache[styleID]
					if !ok {
						definition, styleErr := file.GetStyle(styleID)
						if styleErr == nil && definition != nil {
							style = canonicalStyleFromXLSX(definition)
							styleCache[styleID] = style
						}
					}
					input.Style = style
				}
				imported.Cells = append(imported.Cells, input)
				parsed.Preview.TotalCells++
				if parsed.Preview.TotalCells > MaxImportCells {
					rows.Close()
					return ParsedWorkbook{}, errors.New("import exceeds one million non-empty cells")
				}
			}
		}
		if err := rows.Error(); err != nil {
			rows.Close()
			return ParsedWorkbook{}, err
		}
		rows.Close()
		storedBeforeMerges := len(imported.Cells)
		// 이름 있는 표. 열 이름으로 가리키는 수식이 파일 안에 있으므로,
		// 표를 버리면 그 칸이 모두 #NAME? 이 된다.
		if fileTables, tableErr := file.GetTables(sheetName); tableErr == nil {
			for _, fileTable := range fileTables {
				headerRow := fileTable.ShowHeaderRow == nil || *fileTable.ShowHeaderRow
				imported.Tables = append(imported.Tables, workbook.ImportSheetTable{
					Name: fileTable.Name, Range: fileTable.Range, HeaderRow: headerRow,
				})
			}
		}
		merges, mergeErr := file.GetMergeCells(sheetName)
		if mergeErr != nil {
			return ParsedWorkbook{}, fmt.Errorf("read merged cells from sheet %s: %w", sheetName, mergeErr)
		}
		if err := applyImportedMerges(&imported, merges, &rowIndex, &maxColumns); err != nil {
			return ParsedWorkbook{}, fmt.Errorf("import merged cells from sheet %s: %w", sheetName, err)
		}
		parsed.Preview.TotalCells += len(imported.Cells) - storedBeforeMerges
		if parsed.Preview.TotalCells > MaxImportCells {
			return ParsedWorkbook{}, errors.New("import exceeds one million stored cells")
		}
		comments, commentErr := file.GetComments(sheetName)
		if commentErr != nil {
			return ParsedWorkbook{}, fmt.Errorf("read comments from sheet %s: %w", sheetName, commentErr)
		}
		storedBeforeComments := len(imported.Cells)
		if err := applyImportedComments(&imported, comments, &rowIndex, &maxColumns); err != nil {
			return ParsedWorkbook{}, fmt.Errorf("import comments from sheet %s: %w", sheetName, err)
		}
		parsed.Preview.TotalCells += len(imported.Cells) - storedBeforeComments
		if parsed.Preview.TotalCells > MaxImportCells {
			return ParsedWorkbook{}, errors.New("import exceeds one million stored cells")
		}
		imported.Layout = readSheetLayout(file, sheetName, rowIndex, maxColumns)
		imported.Validations = importValidations(file, sheetName)
		imported.ConditionalFormats = importConditionalFormats(file, sheetName)
		parsed.Sheets = append(parsed.Sheets, imported)
		parsed.Preview.Sheets = append(parsed.Preview.Sheets, SheetPreview{Name: sheetName, Rows: rowIndex, Columns: maxColumns, NonEmptyCells: len(imported.Cells)})
	}
	known := make(map[string]struct{}, len(sheetNames))
	for _, sheetName := range sheetNames {
		known[sheetName] = struct{}{}
	}
	// 차트는 excelize 가 읽지 못하므로 XML 을 곧바로 읽는다. 자료가 있는
	// 시트에 붙인다 — 그림이 어느 시트 위에 놓여 있었는지는 그리기 관계를
	// 따라가야 알 수 있는데, 자료 옆에 두는 편이 다시 찾기 쉽다.
	charts, unreadCharts := importCharts(archive)
	for _, item := range charts {
		for index := range parsed.Sheets {
			if parsed.Sheets[index].Name == item.SheetName {
				parsed.Sheets[index].Charts = append(parsed.Sheets[index].Charts, item.Chart)
				break
			}
		}
	}
	named, namedFunctions, printAreas, skippedNames := importDefinedNames(file, known)
	parsed.NamedRanges = named
	parsed.NamedFunctions = namedFunctions
	// 인쇄 영역은 이름의 모습으로 담겨 있지만 이름이 아니라 시트의 성질이다.
	// 시트마다 제 자리에 옮겨 둔다.
	for index := range parsed.Sheets {
		area, found := printAreas[parsed.Sheets[index].Name]
		if !found {
			continue
		}
		// 다른 배치 정보가 없는 시트는 Layout 이 비어 있다. 인쇄 영역만
		// 있는 경우에도 담을 자리를 만들어 준다.
		if parsed.Sheets[index].Layout == nil {
			parsed.Sheets[index].Layout = &workbook.SheetLayout{}
		}
		parsed.Sheets[index].Layout.PrintArea = area
	}
	parsed.Preview.Warnings = append(parsed.Preview.Warnings, unsupportedXLSXParts(archive, skippedNames, unreadCharts)...)
	return parsed, nil
}

// importDefinedNames carries workbook-scoped Excel names over. A name whose
// target is not a plain range on a sheet in this file - a print area, a
// constant, a formula, a reference into another workbook - has no kanpic
// equivalent, so it is counted and reported rather than approximated.
func importDefinedNames(file *excelize.File, sheetNames map[string]struct{}) ([]workbook.ImportNamedRange, []workbook.ImportNamedFunction, map[string]string, SkippedNames) {
	definitions := file.GetDefinedName()
	if len(definitions) == 0 {
		return nil, nil, nil, SkippedNames{}
	}
	named := make([]workbook.ImportNamedRange, 0, len(definitions))
	functions := make([]workbook.ImportNamedFunction, 0)
	printAreas := make(map[string]string)
	skipped := SkippedNames{}
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		// 인쇄 영역은 이름의 모습을 하고 있지만 이름이 아니다. 시트 전용
		// 이름과 함께 세되, 세는 자리는 나눠 둔다 — 무엇이 빠졌는지 알아야
		// 사람이 그것을 다시 만들지 말지 정할 수 있다.
		if strings.HasPrefix(definition.Name, "_xlnm.") {
			// 인쇄 영역은 이름의 모습을 하고 있지만 시트의 성질이다. 이제
			// 시트로 옮겨 담으므로 세지 않는다. 그 밖의 _xlnm 이름 —
			// 반복할 제목 행 같은 것 — 은 아직 갈 곳이 없어 센다.
			if definition.Name == "_xlnm.Print_Area" {
				if sheetName, area, ok := splitDefinedNameTarget(definition.RefersTo); ok {
					if _, exists := sheetNames[sheetName]; exists {
						printAreas[sheetName] = area
						continue
					}
				}
			}
			skipped.PrintArea++
			continue
		}
		// A sheet-scoped name means something different from a workbook-scoped
		// one, and kanpic only has the workbook-scoped kind. excelize reports
		// that scope as the literal "Workbook".
		if definition.Scope != "Workbook" {
			skipped.SheetScoped++
			continue
		}
		sheetName, area, ok := splitDefinedNameTarget(definition.RefersTo)
		if !ok {
			// LAMBDA 를 가리키는 이름은 kanpic 의 이름 있는 수식이다. 범위가
			// 아니라고 버리면 내보낸 파일을 도로 열 때 이름이 사라진다.
			if item, isFunction := namedFunctionFromDefinedName(definition.Name, definition.RefersTo); isFunction {
				key := strings.ToUpper(definition.Name)
				if _, duplicate := seen[key]; duplicate {
					skipped.Duplicate++
					continue
				}
				seen[key] = struct{}{}
				functions = append(functions, item)
				continue
			}
			// 상수나 수식을 가리키는 이름. kanpic 의 이름은 범위만 가리킨다.
			skipped.NotARange++
			continue
		}
		if _, exists := sheetNames[sheetName]; !exists {
			skipped.MissingSheet++
			continue
		}
		key := strings.ToUpper(definition.Name)
		if _, duplicate := seen[key]; duplicate {
			skipped.Duplicate++
			continue
		}
		seen[key] = struct{}{}
		named = append(named, workbook.ImportNamedRange{Name: definition.Name, SheetName: sheetName, Range: area})
	}
	return named, functions, printAreas, skipped
}

// SkippedNames counts, by reason, the defined names an import left behind.
type SkippedNames struct {
	SheetScoped  int
	PrintArea    int
	NotARange    int
	MissingSheet int
	Duplicate    int
}

func (s SkippedNames) Total() int {
	return s.SheetScoped + s.PrintArea + s.NotARange + s.MissingSheet + s.Duplicate
}

// Reasons lists what was left behind, in the words a person would use.
func (s SkippedNames) Reasons() []string {
	reasons := make([]string, 0, 5)
	for _, item := range []struct {
		count int
		label string
	}{
		{s.SheetScoped, "시트 전용"},
		{s.PrintArea, "인쇄 영역"},
		{s.NotARange, "값·수식을 가리키는 이름"},
		{s.MissingSheet, "없는 시트를 가리키는 이름"},
		{s.Duplicate, "이름이 겹치는 것"},
	} {
		if item.count > 0 {
			reasons = append(reasons, fmt.Sprintf("%s %d개", item.label, item.count))
		}
	}
	return reasons
}

// splitDefinedNameTarget reads the sheet and the A1 range out of a RefersTo
// such as `Sheet1!$C$1:$C$2` or `'2분기 실적'!$A$1`.
func splitDefinedNameTarget(refersTo string) (string, string, bool) {
	target := strings.TrimSpace(refersTo)
	target = strings.TrimPrefix(target, "=")
	if target == "" || strings.ContainsAny(target, "[]") {
		return "", "", false
	}
	// A quoted sheet name doubles any apostrophe it contains, so the closing
	// quote is the last one before the "!" that separates sheet from range.
	separator := strings.LastIndex(target, "!")
	if separator <= 0 || separator == len(target)-1 {
		return "", "", false
	}
	sheetName, area := target[:separator], strings.ReplaceAll(target[separator+1:], "$", "")
	if strings.HasPrefix(sheetName, "'") {
		if !strings.HasSuffix(sheetName, "'") || len(sheetName) < 3 {
			return "", "", false
		}
		sheetName = strings.ReplaceAll(sheetName[1:len(sheetName)-1], "''", "'")
	}
	// A name pointing at a union, a constant or a formula is not a range.
	if sheetName == "" || strings.ContainsAny(area, "!,+-*/() '\"") {
		return "", "", false
	}
	if _, err := cellrange.Parse(area); err != nil {
		return "", "", false
	}
	return sheetName, area, true
}

// unsupportedXLSXParts names what the file carries and the import does not. The
// reader already knows: a workbook that arrives without its charts and its
// names looks like the import worked and reads like a different workbook.
func unsupportedXLSXParts(archive *zip.Reader, skippedNames SkippedNames, unreadCharts int) []string {
	counts := map[string]int{}
	for _, entry := range archive.File {
		switch {
		case strings.HasPrefix(entry.Name, "xl/charts/chart") && strings.HasSuffix(entry.Name, ".xml"):
			counts["chart"]++
		case strings.HasPrefix(entry.Name, "xl/pivotTables/pivotTable") && strings.HasSuffix(entry.Name, ".xml"):
			counts["pivot"]++
		case strings.HasPrefix(entry.Name, "xl/media/"):
			counts["media"]++
		case strings.HasPrefix(entry.Name, "xl/externalLinks/externalLink") && strings.HasSuffix(entry.Name, ".xml"):
			counts["external"]++
		case entry.Name == "xl/vbaProject.bin":
			counts["macro"]++
		}
	}
	warnings := make([]string, 0, 5)
	if total := skippedNames.Total(); total > 0 {
		warnings = append(warnings, fmt.Sprintf("이름 정의 %d개는 가져오지 않습니다: %s. kanpic의 이름은 워크북 전체에 걸린 범위만 가리킵니다.", total, strings.Join(skippedNames.Reasons(), ", ")))
	}
	// 이제 대부분의 차트는 가져온다. 되살리지 못한 것만 세어 알린다 —
	// 여러 시트에서 끌어 왔거나 떨어진 자리를 가리키는 차트는 범위 하나로
	// 묶을 수 없어 엉뚱한 그림이 되기 쉽다.
	if unreadCharts > 0 {
		warnings = append(warnings, fmt.Sprintf("차트 %d개는 가져오지 않습니다. 여러 시트나 떨어진 범위를 가리키는 차트는 되살리지 못합니다. 원본 데이터는 그대로 들어옵니다.", unreadCharts))
	}
	if counts["pivot"] > 0 {
		warnings = append(warnings, fmt.Sprintf("피벗 테이블 %d개는 가져오지 않습니다. 계산된 값은 셀로 남습니다.", counts["pivot"]))
	}
	if counts["media"] > 0 {
		warnings = append(warnings, fmt.Sprintf("이미지 %d개는 가져오지 않습니다.", counts["media"]))
	}
	if counts["external"] > 0 {
		warnings = append(warnings, fmt.Sprintf("다른 파일을 참조하는 연결 %d개는 가져오지 않습니다. 마지막으로 저장된 값이 셀에 남습니다.", counts["external"]))
	}
	if counts["macro"] > 0 {
		warnings = append(warnings, "매크로(VBA)는 가져오지 않습니다.")
	}
	return warnings
}

func (s *Service) Export(ctx context.Context, request ExportRequest) (ExportedFile, error) {
	wb, err := s.repository.GetWorkbook(ctx, request.WorkbookID)
	if err != nil {
		return ExportedFile{}, err
	}
	format := strings.ToLower(request.Format)
	switch format {
	case "csv", "tsv":
		return s.exportDelimited(ctx, wb, request.SheetID, format)
	case "json":
		return s.exportJSON(ctx, wb)
	case "xlsx":
		return s.exportXLSX(ctx, wb)
	default:
		return ExportedFile{}, fmt.Errorf("unsupported export format %q", request.Format)
	}
}

func (s *Service) exportDelimited(ctx context.Context, wb workbook.Workbook, sheetID, format string) (ExportedFile, error) {
	sheet, err := selectSheet(wb, sheetID)
	if err != nil {
		return ExportedFile{}, err
	}
	cells, err := s.repository.ReadAllCells(ctx, sheet.ID)
	if err != nil {
		return ExportedFile{}, err
	}
	maxRow, maxColumn := usedDimensions(cells)
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if format == "tsv" {
		writer.Comma = '\t'
	}
	byCoordinate := indexCells(cells)
	for row := 1; row <= maxRow; row++ {
		record := make([]string, maxColumn)
		for column := 1; column <= maxColumn; column++ {
			cell, ok := byCoordinate[coordinateKey(row, column)]
			if !ok {
				continue
			}
			record[column-1] = safeDelimitedValue(cell.Value)
		}
		if err := writer.Write(record); err != nil {
			return ExportedFile{}, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return ExportedFile{}, err
	}
	extension := format
	contentType := "text/csv; charset=utf-8"
	if format == "tsv" {
		contentType = "text/tab-separated-values; charset=utf-8"
	}
	return ExportedFile{Name: safeFileName(wb.Title) + "-" + safeFileName(sheet.Name) + "." + extension, ContentType: contentType, Data: buffer.Bytes()}, nil
}

func (s *Service) exportJSON(ctx context.Context, wb workbook.Workbook) (ExportedFile, error) {
	type jsonSheet struct {
		Name  string          `json:"name"`
		Cells []workbook.Cell `json:"cells"`
	}
	document := struct {
		WorkbookID string      `json:"workbook_id"`
		Title      string      `json:"title"`
		Sheets     []jsonSheet `json:"sheets"`
	}{WorkbookID: wb.ID, Title: wb.Title, Sheets: make([]jsonSheet, 0, len(wb.Sheets))}
	for _, sheet := range wb.Sheets {
		cells, err := s.repository.ReadAllCells(ctx, sheet.ID)
		if err != nil {
			return ExportedFile{}, err
		}
		document.Sheets = append(document.Sheets, jsonSheet{Name: sheet.Name, Cells: cells})
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return ExportedFile{}, err
	}
	return ExportedFile{Name: safeFileName(wb.Title) + ".json", ContentType: "application/json", Data: data}, nil
}

func (s *Service) exportXLSX(ctx context.Context, wb workbook.Workbook) (ExportedFile, error) {
	file := excelize.NewFile()
	defer file.Close()
	defaultSheet := file.GetSheetName(0)
	styleCache := make(map[string]int)
	for index, sheet := range wb.Sheets {
		name := sanitizeSheetName(sheet.Name, index)
		if index == 0 {
			if err := file.SetSheetName(defaultSheet, name); err != nil {
				return ExportedFile{}, err
			}
		} else {
			if _, err := file.NewSheet(name); err != nil {
				return ExportedFile{}, err
			}
		}
		if err := applySheetLayout(file, name, sheet.Layout); err != nil {
			return ExportedFile{}, err
		}
		// Input rules travel with the sheet: a file whose dropdowns are gone
		// looks the same and behaves differently.
		// Conditional formats are grouped by range: Excel stores one rule list
		// per range, while kanpic stores each rule with its own.
		conditionals, err := s.repository.ListConditionalFormats(ctx, sheet.ID)
		if err != nil {
			return ExportedFile{}, err
		}
		styleFor := func(raw json.RawMessage) *int {
			definition := xlsxStyle(raw)
			if definition == nil {
				return nil
			}
			styleData, _ := json.Marshal(definition)
			key := "cf:" + string(styleData)
			styleID, exists := styleCache[key]
			if !exists {
				created, styleErr := file.NewStyle(definition)
				if styleErr != nil {
					return nil
				}
				styleCache[key] = created
				styleID = created
			}
			return &styleID
		}
		byRange := make(map[string][]excelize.ConditionalFormatOptions)
		rangeOrder := make([]string, 0, len(conditionals))
		for _, rule := range conditionals {
			options := exportConditionalFormat(rule, styleFor)
			if options == nil {
				continue
			}
			if _, seen := byRange[rule.Range]; !seen {
				rangeOrder = append(rangeOrder, rule.Range)
			}
			byRange[rule.Range] = append(byRange[rule.Range], *options)
		}
		for _, area := range rangeOrder {
			if err := file.SetConditionalFormat(name, area, byRange[area]); err != nil {
				return ExportedFile{}, err
			}
		}
		rules, err := s.repository.ListDataValidations(ctx, sheet.ID)
		if err != nil {
			return ExportedFile{}, err
		}
		for _, rule := range rules {
			if dv := exportValidation(rule); dv != nil {
				if err := file.AddDataValidation(name, dv); err != nil {
					return ExportedFile{}, err
				}
			}
		}
		cells, err := s.repository.ReadAllCells(ctx, sheet.ID)
		if err != nil {
			return ExportedFile{}, err
		}
		mergedRanges := make(map[string]workbook.MergeMetadata)
		for _, cell := range cells {
			// Dynamic-array child cells are cached server results. Writing them as
			// ordinary XLSX values would block the anchor formula from spilling
			// when Excel or another compatible engine recalculates the workbook.
			if cell.SpillSource != "" {
				continue
			}
			coordinate := cellrange.Address(cell.Row, cell.Column)
			var value any
			if len(cell.Value) > 0 {
				_ = json.Unmarshal(cell.Value, &value)
			}
			if err := file.SetCellValue(name, coordinate, value); err != nil {
				return ExportedFile{}, err
			}
			if cell.Formula != "" {
				// 엑셀은 2007 이후에 생긴 함수를 파일 안에서 _xlfn. 이 붙은
				// 이름으로 기대한다. 붙이지 않고 내보내면 IFS·XLOOKUP 같은
				// 함수가 엑셀에서 모두 #NAME? 이 된다.
				if err := file.SetCellFormula(name, coordinate, formula.ForExcel(cell.Formula)); err != nil {
					return ExportedFile{}, err
				}
			}
			if metadata, merged, mergeErr := workbook.CellMerge(cell); mergeErr != nil {
				return ExportedFile{}, mergeErr
			} else if merged {
				mergedRanges[fmt.Sprintf("%d:%d:%d:%d", metadata.StartRow, metadata.StartColumn, metadata.EndRow, metadata.EndColumn)] = metadata
			}
			// A note is the whole point of the cell it hangs on: "이 수치는 추정".
			// Excel keeps it as a cell comment, and a file that arrives without
			// one looks the same and says less.
			if note := trimComment(cell.Note); note != "" {
				if err := file.AddComment(name, excelize.Comment{Cell: coordinate, Author: exportCommentAuthor, Text: note}); err != nil {
					return ExportedFile{}, err
				}
			}
			if styleDefinition := xlsxStyle(cell.Style); styleDefinition != nil {
				styleData, _ := json.Marshal(styleDefinition)
				key := string(styleData)
				styleID, exists := styleCache[key]
				if !exists {
					styleID, err = file.NewStyle(styleDefinition)
					if err == nil {
						styleCache[key] = styleID
					}
				}
				if styleID > 0 {
					_ = file.SetCellStyle(name, coordinate, coordinate, styleID)
				}
			}
		}
		mergeKeys := make([]string, 0, len(mergedRanges))
		for key := range mergedRanges {
			mergeKeys = append(mergeKeys, key)
		}
		sort.Strings(mergeKeys)
		for _, key := range mergeKeys {
			metadata := mergedRanges[key]
			if err := file.MergeCell(name, cellrange.Address(metadata.StartRow, metadata.StartColumn), cellrange.Address(metadata.EndRow, metadata.EndColumn)); err != nil {
				return ExportedFile{}, err
			}
		}
	}
	// Charts are added after every sheet exists, because a chart names the
	// sheet its data lives on and Excel refuses a reference to a sheet that is
	// not there yet.
	sheetNames := make(map[string]string, len(wb.Sheets))
	for index, sheet := range wb.Sheets {
		sheetNames[sheet.ID] = sanitizeSheetName(sheet.Name, index)
	}
	// 이름을 가진 표도 함께 내보낸다. =SUM(매출표[금액]) 이 든 파일을 표
	// 없이 내보내면 엑셀에서 그 칸이 모두 #NAME? 이 된다.
	sheetTables, err := s.repository.ListSheetTables(ctx, wb.ID)
	if err != nil {
		return ExportedFile{}, err
	}
	for _, item := range sheetTables {
		sheetName, known := sheetNames[item.SheetID]
		if !known {
			continue
		}
		headerRow := item.HeaderRow
		// 합계 줄은 표 범위에서 빼고 내보낸다.
		//
		// 파일 안의 표에도 합계 줄이라는 것이 있지만 지금 쓰는 라이브러리는
		// 그것을 적어 주지 못한다. 합계 줄을 범위에 넣은 채 내보내면, 도로
		// 열었을 때 그 줄이 자료가 되어 =SUM(매출표[금액]) 이 제 자신을
		// 더한다 — #CIRC! 다.
		//
		// 빼서 내보내면 합계 칸은 표 바로 아래의 보통 칸이 된다. 엑셀에서도
		// 그대로 셈하고, 도로 가져와도 표가 그 줄을 삼키지 않는다.
		exportRange := tableExportRange(item)
		if exportRange == "" {
			continue
		}
		// 엑셀의 표는 머리글이 있든 없든 이름으로 가리킬 수 있다. 하나가
		// 받아들여지지 않는다고 내보내기 전체를 막지는 않는다.
		_ = file.AddTable(sheetName, &excelize.Table{
			Range: exportRange, Name: item.Name, StyleName: excelTableStyle(item.Theme),
			ShowHeaderRow: &headerRow, ShowRowStripes: &headerRow,
		})
	}
	// A named range is how a sheet explains itself: =SUM(단가) reads and a file
	// that arrives without the name turns every formula using it into #NAME?.
	named, err := s.repository.ListNamedRanges(ctx, wb.ID)
	if err != nil {
		return ExportedFile{}, err
	}
	for _, item := range named {
		sheetName, known := sheetNames[item.SheetID]
		if !known {
			continue
		}
		refersTo, ok := definedNameTarget(sheetName, item.Range)
		if !ok {
			continue
		}
		// Excel is stricter about what a name may be than kanpic is. One name it
		// refuses must not cost the whole export.
		_ = file.SetDefinedName(&excelize.DefinedName{Name: item.Name, RefersTo: refersTo})
	}
	// 이름 있는 수식은 엑셀의 LAMBDA 정의된 이름으로 나간다. 그러지 않으면
	// =마진율(A1,B1) 이 엑셀에서 #NAME? 이 된다 — kanpic 안에서는 되는데
	// 파일로 꺼내면 깨지는 것이야말로 사람을 놀라게 한다.
	namedFunctions, err := s.repository.ListNamedFunctions(ctx, wb.ID)
	if err != nil {
		return ExportedFile{}, err
	}
	for _, item := range namedFunctions {
		refersTo, ok := lambdaDefinedName(item)
		if !ok {
			continue
		}
		// 엑셀이 받아 주지 않는 이름 하나가 내보내기 전체를 막으면 안 된다.
		_ = file.SetDefinedName(&excelize.DefinedName{Name: item.Name, RefersTo: refersTo})
	}
	charts, err := s.repository.ListCharts(ctx, wb.ID, "")
	if err != nil {
		return ExportedFile{}, err
	}
	for _, item := range charts {
		target, known := sheetNames[item.SheetID]
		source, sourceKnown := sheetNames[item.SourceSheetID]
		if !known || !sourceKnown || item.SourceRange == "#REF!" {
			continue
		}
		primary, combo := exportChart(item, source)
		if primary == nil {
			continue
		}
		if combo != nil {
			if err := file.AddChart(target, anchorCell(item.Position), primary, combo); err != nil {
				return ExportedFile{}, err
			}
			continue
		}
		if err := file.AddChart(target, anchorCell(item.Position), primary); err != nil {
			return ExportedFile{}, err
		}
	}
	var buffer bytes.Buffer
	if err := file.Write(&buffer); err != nil {
		return ExportedFile{}, err
	}
	return ExportedFile{Name: safeFileName(wb.Title) + ".xlsx", ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Data: buffer.Bytes()}, nil
}

// applyImportedComments carries Excel cell comments over as kanpic notes. A
// note can sit on an otherwise empty cell, which the row reader skips, so this
// runs over the whole comment list rather than over the cells already read.
// definedNameTarget writes an A1 range the way a workbook-scoped Excel name
// has to: sheet-qualified and absolute, with the sheet name quoted when it
// carries anything but letters and digits.
func definedNameTarget(sheetName, area string) (string, bool) {
	selected, err := cellrange.Parse(area)
	if err != nil {
		return "", false
	}
	quoted := sheetName
	if strings.ContainsAny(sheetName, " '!\"") || strings.Contains(sheetName, "-") {
		quoted = "'" + strings.ReplaceAll(sheetName, "'", "''") + "'"
	}
	start := absoluteAddress(selected.Start.Row, selected.Start.Column)
	end := absoluteAddress(selected.End.Row, selected.End.Column)
	if start == end {
		return quoted + "!" + start, true
	}
	return quoted + "!" + start + ":" + end, true
}

func absoluteAddress(row, column int) string {
	address := cellrange.Address(row, column)
	for index := 0; index < len(address); index++ {
		if address[index] >= '0' && address[index] <= '9' {
			return "$" + address[:index] + "$" + address[index:]
		}
	}
	return "$" + address
}

func applyImportedComments(imported *workbook.ImportSheet, comments []excelize.Comment, maxRow, maxColumn *int) error {
	if len(comments) == 0 {
		return nil
	}
	byCoordinate := make(map[string]int, len(imported.Cells))
	for index, input := range imported.Cells {
		byCoordinate[coordinateKey(input.Row, input.Column)] = index
	}
	for _, comment := range comments {
		note := trimComment(commentText(comment))
		if note == "" {
			continue
		}
		column, row, err := excelize.CellNameToCoordinates(comment.Cell)
		if err != nil {
			return err
		}
		if row > maxLayoutRow || column > maxLayoutColumn {
			continue
		}
		if index, exists := byCoordinate[coordinateKey(row, column)]; exists {
			imported.Cells[index].Note = note
			continue
		}
		byCoordinate[coordinateKey(row, column)] = len(imported.Cells)
		imported.Cells = append(imported.Cells, workbook.CellInput{Row: row, Column: column, Note: note})
		if row > *maxRow {
			*maxRow = row
		}
		if column > *maxColumn {
			*maxColumn = column
		}
	}
	sort.Slice(imported.Cells, func(i, j int) bool {
		if imported.Cells[i].Row == imported.Cells[j].Row {
			return imported.Cells[i].Column < imported.Cells[j].Column
		}
		return imported.Cells[i].Row < imported.Cells[j].Row
	})
	return nil
}

// commentText prefers the plain text and falls back to the rich-text runs,
// because Excel writes a formatted comment only as paragraphs.
func commentText(comment excelize.Comment) string {
	if text := strings.TrimSpace(comment.Text); text != "" {
		return text
	}
	var builder strings.Builder
	for _, run := range comment.Paragraph {
		builder.WriteString(run.Text)
	}
	return strings.TrimSpace(builder.String())
}

func applyImportedMerges(imported *workbook.ImportSheet, merges []excelize.MergeCell, maxRow, maxColumn *int) error {
	byCoordinate := make(map[string]workbook.CellInput, len(imported.Cells))
	for _, input := range imported.Cells {
		byCoordinate[coordinateKey(input.Row, input.Column)] = input
	}
	for _, merged := range merges {
		selected, err := cellrange.Parse(merged.GetStartAxis() + ":" + merged.GetEndAxis())
		if err != nil {
			return err
		}
		rows, columns := selected.End.Row-selected.Start.Row+1, selected.End.Column-selected.Start.Column+1
		if rows < 1 || columns < 1 || rows > workbook.MaxPasteCells || columns > workbook.MaxPasteCells || rows > workbook.MaxPasteCells/columns {
			return fmt.Errorf("merged range must contain 1 to %d cells", workbook.MaxPasteCells)
		}
		existing := make([]workbook.Cell, 0, rows*columns)
		for row := selected.Start.Row; row <= selected.End.Row; row++ {
			for column := selected.Start.Column; column <= selected.End.Column; column++ {
				if input, exists := byCoordinate[coordinateKey(row, column)]; exists {
					existing = append(existing, workbook.Cell{Row: row, Column: column, Value: input.Value, Formula: input.Formula, Style: input.Style})
				}
			}
		}
		inputs, err := workbook.BuildMergeCells(existing, selected, true)
		if err != nil {
			return err
		}
		for _, input := range inputs {
			byCoordinate[coordinateKey(input.Row, input.Column)] = input
		}
		if selected.End.Row > *maxRow {
			*maxRow = selected.End.Row
		}
		if selected.End.Column > *maxColumn {
			*maxColumn = selected.End.Column
		}
	}
	imported.Cells = imported.Cells[:0]
	for _, input := range byCoordinate {
		imported.Cells = append(imported.Cells, input)
	}
	sort.Slice(imported.Cells, func(i, j int) bool {
		if imported.Cells[i].Row == imported.Cells[j].Row {
			return imported.Cells[i].Column < imported.Cells[j].Column
		}
		return imported.Cells[i].Row < imported.Cells[j].Row
	})
	return nil
}

func detectDelimiter(data []byte) rune {
	line := string(data)
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	candidates := []rune{',', ';', '\t'}
	best, bestCount := ',', -1
	for _, candidate := range candidates {
		count := strings.Count(line, string(candidate))
		if count > bestCount {
			best, bestCount = candidate, count
		}
	}
	return best
}
func parseScalar(value string) any {
	// A delimited file that came from a spreadsheet keeps formula-looking text
	// behind an apostrophe. Reading it as part of the value turns a phone
	// number into '+82-10-1234-5678 on the way back in.
	value = unguardDelimitedValue(value)
	lower := strings.ToLower(value)
	if lower == "true" {
		return true
	}
	if lower == "false" {
		return false
	}
	if !hasSignificantLeadingZero(value) {
		if number, err := strconv.ParseFloat(value, 64); err == nil {
			return number
		}
	}
	return value
}
func parseXLSXValue(value string, cellType excelize.CellType) any {
	switch cellType {
	case excelize.CellTypeUnset:
		// XLSX leaves the type attribute off numbers; text is always stored as
		// a shared or inline string. An untyped cell holding digits is a
		// number, and reading it as words means SUM over an imported column
		// quietly answers zero.
		if value == "" || hasSignificantLeadingZero(value) {
			return value
		}
		if number, err := strconv.ParseFloat(value, 64); err == nil {
			return number
		}
	case excelize.CellTypeBool:
		boolean, err := strconv.ParseBool(value)
		if err == nil {
			return boolean
		}
	case excelize.CellTypeNumber:
		number, err := strconv.ParseFloat(value, 64)
		if err == nil {
			return number
		}
	}
	return value
}
func hasSignificantLeadingZero(value string) bool {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	return len(trimmed) > 1 && trimmed[0] == '0' && trimmed[1] >= '0' && trimmed[1] <= '9' && !strings.Contains(trimmed, ".")
}
func selectSheet(wb workbook.Workbook, id string) (workbook.Sheet, error) {
	if id == "" && len(wb.Sheets) > 0 {
		return wb.Sheets[0], nil
	}
	for _, sheet := range wb.Sheets {
		if sheet.ID == id {
			return sheet, nil
		}
	}
	return workbook.Sheet{}, workbook.ErrNotFound
}
func usedDimensions(cells []workbook.Cell) (int, int) {
	rows, columns := 0, 0
	for _, cell := range cells {
		if cell.Row > rows {
			rows = cell.Row
		}
		if cell.Column > columns {
			columns = cell.Column
		}
	}
	return rows, columns
}
func indexCells(cells []workbook.Cell) map[string]workbook.Cell {
	result := make(map[string]workbook.Cell, len(cells))
	for _, cell := range cells {
		result[coordinateKey(cell.Row, cell.Column)] = cell
	}
	return result
}
func coordinateKey(row, column int) string { return strconv.Itoa(row) + ":" + strconv.Itoa(column) }
func safeDelimitedValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	text := delimitedNumberText(value)
	if _, isString := value.(string); isString && needsDelimitedGuard(text) {
		return "'" + text
	}
	return text
}

// delimitedNumberText writes a number the way a spreadsheet does. The default
// formatting turns 12345678901234 into 1.2345678901234e+13, which reads back
// as the same number here but is not what anyone opening the file expects to
// see. Values too large or too small to write plainly keep the exponent
// rather than becoming a wall of zeroes.
func delimitedNumberText(value any) string {
	number, isNumber := value.(float64)
	if !isNumber {
		return fmt.Sprint(value)
	}
	plain := strconv.FormatFloat(number, 'f', -1, 64)
	if len(plain) <= 21 {
		return plain
	}
	return strconv.FormatFloat(number, 'g', -1, 64)
}

// A leading =, +, - or @ makes a spreadsheet treat the cell as a formula when
// the file is opened, so those values go out behind an apostrophe. Text that
// already looks guarded is guarded again, which is what lets the reader take
// exactly one apostrophe off and land back on the value that was written.
func needsDelimitedGuard(text string) bool {
	if text == "" {
		return false
	}
	if strings.ContainsRune("=+-@", rune(text[0])) {
		return true
	}
	return text[0] == '\'' && len(text) > 1 && strings.ContainsRune("=+-@", rune(text[1]))
}

// unguardDelimitedValue undoes that. Without it a phone number written as
// +82-10-1234-5678 comes back from its own export as '+82-10-1234-5678.
func unguardDelimitedValue(text string) string {
	if len(text) > 1 && text[0] == '\'' && needsDelimitedGuard(text[1:]) {
		return text[1:]
	}
	return text
}
func safeFileName(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\x00", "")
	value = replacer.Replace(value)
	if value == "" {
		return "kanpic"
	}
	return value
}
func sanitizeSheetName(value string, index int) string {
	value = strings.TrimSpace(value)
	for _, character := range []string{"\\", "/", "?", "*", "[", "]", ":"} {
		value = strings.ReplaceAll(value, character, "_")
	}
	runes := []rune(value)
	if len(runes) > 31 {
		value = string(runes[:31])
	}
	if value == "" {
		value = fmt.Sprintf("Sheet%d", index+1)
	}
	return value
}
