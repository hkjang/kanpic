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

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

const (
	DefaultMaxUploadBytes   = 20 << 20
	DefaultMaxExpandedBytes = 200 << 20
	MaxImportCells          = 1_000_000
)

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
	Title   string
	Format  string
	Sheets  []workbook.ImportSheet
	Preview Preview
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
	return s.repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{WorkspaceID: request.WorkspaceID, Title: parsed.Title, OwnerID: request.ActorID, ActorID: request.ActorID, IdempotencyKey: request.IdempotencyKey, FileName: request.FileName, Format: parsed.Format, Sheets: parsed.Sheets})
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
				formula, _ := file.GetCellFormula(sheetName, coordinate)
				if displayValue == "" && formula == "" {
					continue
				}
				cellType, _ := file.GetCellType(sheetName, coordinate)
				value := parseXLSXValue(displayValue, cellType)
				encoded, _ := json.Marshal(value)
				input := workbook.CellInput{Row: rowIndex, Column: columnIndex + 1, Value: encoded, Formula: formula}
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
		imported.Layout = readSheetLayout(file, sheetName, rowIndex, maxColumns)
		parsed.Sheets = append(parsed.Sheets, imported)
		parsed.Preview.Sheets = append(parsed.Preview.Sheets, SheetPreview{Name: sheetName, Rows: rowIndex, Columns: maxColumns, NonEmptyCells: len(imported.Cells)})
	}
	return parsed, nil
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
				if err := file.SetCellFormula(name, coordinate, cell.Formula); err != nil {
					return ExportedFile{}, err
				}
			}
			if metadata, merged, mergeErr := workbook.CellMerge(cell); mergeErr != nil {
				return ExportedFile{}, mergeErr
			} else if merged {
				mergedRanges[fmt.Sprintf("%d:%d:%d:%d", metadata.StartRow, metadata.StartColumn, metadata.EndRow, metadata.EndColumn)] = metadata
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
	var buffer bytes.Buffer
	if err := file.Write(&buffer); err != nil {
		return ExportedFile{}, err
	}
	return ExportedFile{Name: safeFileName(wb.Title) + ".xlsx", ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Data: buffer.Bytes()}, nil
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
	text := fmt.Sprint(value)
	if _, isString := value.(string); isString && text != "" && strings.ContainsRune("=+-@", rune(text[0])) {
		return "'" + text
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
