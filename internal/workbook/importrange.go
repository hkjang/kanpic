package workbook

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

// MaxWorkbookImports bounds how many distinct cross-workbook blocks one
// workbook pulls in. Every one of them is fetched on each recalculation, so
// the ceiling is what keeps a runaway sheet from turning one edit into a
// hundred reads.
const MaxWorkbookImports = 50

// importReader is the slice of a repository IMPORTRANGE needs: who governs the
// importing workbook, whether that person may read the source, and the cells
// themselves. Both repositories implement it against their own storage so the
// resolution rules below live in exactly one place.
type importReader interface {
	importOwner(workbookID string) (string, error)
	importTitle(workbookID string) (string, error)
	importSheet(sourceWorkbookID, sheetName string) (string, error)
	importReadable(sourceWorkbookID, ownerID string) (bool, error)
	importCells(sheetID string, selected cellrange.Range) ([]Cell, error)
}

// collectImportRequests finds every IMPORTRANGE call the calculation is about
// to evaluate. Formulas arriving with the mutation count too: the cell someone
// just typed is the one they are waiting on.
func collectImportRequests(cells map[string]map[cellKey]Cell, submitted []CellInput) []formula.ImportRequest {
	seen := make(map[string]struct{})
	requests := make([]formula.ImportRequest, 0, 4)
	add := func(text string) {
		if text == "" {
			return
		}
		for _, request := range formula.ImportRequests(text) {
			key := formula.ImportKey(request.Source, request.Range)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			requests = append(requests, request)
		}
	}
	for _, sheet := range cells {
		for _, cell := range sheet {
			add(cell.Formula)
		}
	}
	for _, input := range submitted {
		add(input.Formula)
	}
	return requests
}

// parseImportSource accepts either a workbook identifier or the editor URL a
// user copies out of the address bar, which is what people actually paste.
func parseImportSource(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if index := strings.Index(value, "/workbooks/"); index >= 0 {
		value = value[index+len("/workbooks/"):]
	}
	if index := strings.IndexAny(value, "/?#"); index >= 0 {
		value = value[:index]
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return "", false
	}
	return value, true
}

// parseImportArea splits "Sheet1!A1:C10" into its sheet name and range. The
// sheet name may be left out, which means the source workbook's first sheet.
func parseImportArea(value string) (string, cellrange.Range, bool) {
	value = strings.TrimSpace(value)
	sheetName := ""
	if index := strings.LastIndex(value, "!"); index >= 0 {
		sheetName = strings.Trim(strings.TrimSpace(value[:index]), "'")
		value = strings.TrimSpace(value[index+1:])
	}
	selected, err := cellrange.Parse(strings.ReplaceAll(value, "$", ""))
	if err != nil {
		return "", cellrange.Range{}, false
	}
	return sheetName, selected, true
}

// resolveImportRequests fetches each requested block and reports the reason
// for every one it cannot. An import that fails silently is worse than one
// that fails loudly: a blank block reads as "the source has no data".
func resolveImportRequests(reader importReader, workbookID string, requests []formula.ImportRequest) map[string]formula.ImportedRange {
	if len(requests) == 0 {
		return nil
	}
	result := make(map[string]formula.ImportedRange, len(requests))
	fail := func(request formula.ImportRequest, code, message string) {
		result[formula.ImportKey(request.Source, request.Range)] = formula.ImportedRange{Err: &formula.Error{Code: code, Message: message}}
	}
	if len(requests) > MaxWorkbookImports {
		for _, request := range requests {
			fail(request, "#REF!", fmt.Sprintf("한 워크북에서 IMPORTRANGE는 최대 %d개까지 사용할 수 있습니다", MaxWorkbookImports))
		}
		return result
	}
	owner, err := reader.importOwner(workbookID)
	if err != nil {
		for _, request := range requests {
			fail(request, "#REF!", "이 워크북의 소유자를 확인할 수 없어 가져오기를 중단했습니다")
		}
		return result
	}
	readable := make(map[string]bool, len(requests))
	for _, request := range requests {
		sourceID, ok := parseImportSource(request.Source)
		if !ok {
			fail(request, "#REF!", "워크북 주소를 알아볼 수 없습니다. 워크북 ID나 편집기 주소를 넣어 주세요")
			continue
		}
		sheetName, selected, ok := parseImportArea(request.Range)
		if !ok {
			fail(request, "#REF!", "가져올 범위를 알아볼 수 없습니다. 예: \"Sheet1!A1:C10\"")
			continue
		}
		count := int64(selected.End.Row-selected.Start.Row+1) * int64(selected.End.Column-selected.Start.Column+1)
		if count > formula.MaxImportedCells {
			fail(request, "#VALUE!", fmt.Sprintf("한 번에 가져올 수 있는 셀은 %d개까지입니다", formula.MaxImportedCells))
			continue
		}
		if sourceID == workbookID {
			fail(request, "#REF!", "같은 워크북은 IMPORTRANGE 없이 시트 이름으로 참조하세요")
			continue
		}
		allowed, cached := readable[sourceID]
		if !cached {
			// The importing workbook's owner is the principal, not whoever
			// happens to be typing: a formula must not hand an editor data the
			// workbook itself was never allowed to see, and it must not stop
			// working when a different person edits the sheet.
			allowed, err = reader.importReadable(sourceID, owner)
			if err != nil {
				fail(request, "#REF!", "원본 워크북을 찾을 수 없습니다")
				continue
			}
			readable[sourceID] = allowed
		}
		if !allowed {
			fail(request, "#REF!", "이 워크북의 소유자에게 원본 워크북 읽기 권한이 없습니다. 원본을 공유한 뒤 다시 계산됩니다")
			continue
		}
		sheetID, err := reader.importSheet(sourceID, sheetName)
		if err != nil {
			fail(request, "#REF!", "원본 워크북에서 시트를 찾을 수 없습니다")
			continue
		}
		cells, err := reader.importCells(sheetID, selected)
		if err != nil {
			fail(request, "#REF!", "원본 데이터를 읽지 못했습니다")
			continue
		}
		result[formula.ImportKey(request.Source, request.Range)] = importedBlock(cells, selected)
	}
	return result
}

// Connection is one IMPORTRANGE target as the connections panel shows it: what
// it points at, whether it can be read right now, and which cells depend on it.
type Connection struct {
	Source     string   `json:"source"`
	Range      string   `json:"range"`
	WorkbookID string   `json:"workbook_id,omitempty"`
	Title      string   `json:"title,omitempty"`
	Status     string   `json:"status"`
	Message    string   `json:"message,omitempty"`
	Rows       int      `json:"rows,omitempty"`
	Columns    int      `json:"columns,omitempty"`
	Cells      []string `json:"cells"`
}

// WorkbookConnections is the list plus the moment it was checked, because the
// status of a connection is only ever true as of a point in time.
type WorkbookConnections struct {
	WorkbookID  string       `json:"workbook_id"`
	Items       []Connection `json:"items"`
	CheckedAt   time.Time    `json:"checked_at"`
	RefreshedAt *time.Time   `json:"refreshed_at,omitempty"`
	Version     int64        `json:"version,omitempty"`
}

// MaxConnectionCells caps how many dependent cell addresses one connection
// reports. The list is there to answer "where is this used", not to be a
// complete index.
const MaxConnectionCells = 20

// describeConnections reports every IMPORTRANGE in the workbook with its
// current state. It runs the same resolution the calculation runs, so the
// panel cannot disagree with what the cells show.
func describeConnections(reader importReader, workbookID string, sheets map[string]Sheet, cells map[string]map[cellKey]Cell, now time.Time) WorkbookConnections {
	requests := collectImportRequests(cells, nil)
	result := WorkbookConnections{WorkbookID: workbookID, Items: make([]Connection, 0, len(requests)), CheckedAt: now}
	if len(requests) == 0 {
		return result
	}
	users := make(map[string][]string, len(requests))
	for sheetID, sheetCells := range cells {
		name := sheets[sheetID].Name
		for key, cell := range sheetCells {
			for _, request := range formula.ImportRequests(cell.Formula) {
				importKey := formula.ImportKey(request.Source, request.Range)
				users[importKey] = append(users[importKey], name+"!"+cellrange.Address(key.row, key.column))
			}
		}
	}
	resolved := resolveImportRequests(reader, workbookID, requests)
	titles := make(map[string]string)
	for _, request := range requests {
		importKey := formula.ImportKey(request.Source, request.Range)
		item := Connection{Source: request.Source, Range: request.Range, Status: "ok"}
		if sourceID, ok := parseImportSource(request.Source); ok {
			item.WorkbookID = sourceID
			title, cached := titles[sourceID]
			if !cached {
				title, _ = reader.importTitle(sourceID)
				titles[sourceID] = title
			}
			item.Title = title
		}
		block := resolved[importKey]
		if block.Err != nil {
			item.Status = "error"
			item.Message = block.Err.Message
		} else {
			item.Rows, item.Columns = block.Rows, block.Columns
		}
		addresses := users[importKey]
		sort.Strings(addresses)
		if len(addresses) > MaxConnectionCells {
			addresses = addresses[:MaxConnectionCells]
		}
		item.Cells = addresses
		result.Items = append(result.Items, item)
	}
	sort.Slice(result.Items, func(i, j int) bool {
		if result.Items[i].Source == result.Items[j].Source {
			return result.Items[i].Range < result.Items[j].Range
		}
		return result.Items[i].Source < result.Items[j].Source
	})
	return result
}

// importedBlock lays the fetched cells out in the shape of the requested range
// so a gap in the source becomes an empty cell rather than a shifted column.
func importedBlock(cells []Cell, selected cellrange.Range) formula.ImportedRange {
	rows := selected.End.Row - selected.Start.Row + 1
	columns := selected.End.Column - selected.Start.Column + 1
	values := make([]any, rows*columns)
	for _, cell := range cells {
		row, column := cell.Row-selected.Start.Row, cell.Column-selected.Start.Column
		if row < 0 || row >= rows || column < 0 || column >= columns {
			continue
		}
		var value any
		if len(cell.Value) > 0 {
			if err := json.Unmarshal(cell.Value, &value); err != nil {
				continue
			}
		}
		values[row*columns+column] = value
	}
	return formula.ImportedRange{Rows: rows, Columns: columns, Values: values}
}
