package workbook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

type SortKey struct {
	Column    int    `json:"column"`
	Direction string `json:"direction"`
}

type SortOptions struct {
	Keys          []SortKey `json:"keys"`
	HeaderRows    int       `json:"header_rows"`
	CaseSensitive bool      `json:"case_sensitive"`
	// LiteralOrder compares text character by character, the way Excel does.
	// The default reads the digits inside a value as a number, so 2월 comes
	// before 10월 and 항목2 before 항목10.
	LiteralOrder bool `json:"literal_order"`
}

type sortScalar struct {
	rank   int
	blank  bool
	number float64
	text   string
	truth  bool
}

type sortRow struct {
	originalRow int
	values      []sortScalar
}

// BuildSortCells deterministically materializes a stable row sort. Every cell
// in the data portion is included so old coordinates are cleared atomically,
// while formulas and ordinary styles move with their source row.
func BuildSortCells(existing []Cell, selected cellrange.Range, options SortOptions) ([]CellInput, error) {
	rows := selected.End.Row - selected.Start.Row + 1
	columns := selected.End.Column - selected.Start.Column + 1
	if rows < 2 || columns < 1 || options.HeaderRows < 0 || options.HeaderRows >= rows {
		return nil, fmt.Errorf("%w: sort range must contain at least two data rows", ErrInvalid)
	}
	dataRows := rows - options.HeaderRows
	if dataRows < 2 || dataRows > MaxSortCells || columns > MaxSortCells || dataRows > MaxSortCells/columns {
		return nil, fmt.Errorf("%w: sorted data range must contain 2 to %d cells", ErrInvalid, MaxSortCells)
	}
	if len(options.Keys) == 0 || len(options.Keys) > columns {
		return nil, fmt.Errorf("%w: sort keys must contain 1 to %d columns", ErrInvalid, columns)
	}
	directions := make([]int, len(options.Keys))
	seenColumns := make(map[int]bool, len(options.Keys))
	for index, key := range options.Keys {
		if key.Column < selected.Start.Column || key.Column > selected.End.Column || seenColumns[key.Column] {
			return nil, fmt.Errorf("%w: sort key column must be unique and inside the selected range", ErrInvalid)
		}
		seenColumns[key.Column] = true
		switch strings.ToLower(strings.TrimSpace(key.Direction)) {
		case "asc":
			directions[index] = 1
		case "desc":
			directions[index] = -1
		default:
			return nil, fmt.Errorf("%w: sort direction must be asc or desc", ErrInvalid)
		}
	}
	byCoordinate := make(map[string]Cell, len(existing))
	dataStart := selected.Start.Row + options.HeaderRows
	for _, cell := range existing {
		byCoordinate[coordinateKey(cell.Row, cell.Column)] = cell
		if cell.Row >= dataStart && cell.Row <= selected.End.Row && cell.Column >= selected.Start.Column && cell.Column <= selected.End.Column {
			if cell.SpillSource != "" {
				return nil, fmt.Errorf("%w: array result cell %s from %s cannot be sorted", ErrInvalid, cellrange.Address(cell.Row, cell.Column), cell.SpillSource)
			}
			if _, merged, err := CellMerge(cell); err != nil {
				return nil, err
			} else if merged {
				return nil, fmt.Errorf("%w: merged cells must be unmerged before sorting", ErrInvalid)
			}
		}
	}
	// Caser 는 여러 고루틴이 같이 쓸 수 없으므로 정렬 한 번마다 하나 만든다.
	folder := newCaseFolder()
	records := make([]sortRow, 0, dataRows)
	for row := dataStart; row <= selected.End.Row; row++ {
		record := sortRow{originalRow: row, values: make([]sortScalar, len(options.Keys))}
		for index, key := range options.Keys {
			value, err := sortableValue(byCoordinate[coordinateKey(row, key.Column)], options.CaseSensitive, folder)
			if err != nil {
				return nil, err
			}
			record.values[index] = value
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(left, right int) bool {
		for index := range options.Keys {
			comparison := compareSortScalars(records[left].values[index], records[right].values[index], options.LiteralOrder)
			if comparison != 0 {
				if records[left].values[index].blank || records[right].values[index].blank {
					return comparison < 0
				}
				return comparison*directions[index] < 0
			}
		}
		return false
	})
	inputs := make([]CellInput, 0, dataRows*columns)
	for offset, record := range records {
		destinationRow := dataStart + offset
		for column := selected.Start.Column; column <= selected.End.Column; column++ {
			source := byCoordinate[coordinateKey(record.originalRow, column)]
			formulaText := source.Formula
			if formulaText != "" {
				formulaText = formula.ShiftReferences(formulaText, destinationRow-record.originalRow, 0)
			}
			inputs = append(inputs, CellInput{Row: destinationRow, Column: column, Value: cloneJSON(source.Value), Formula: formulaText, Style: cloneJSON(source.Style), SpillSource: source.SpillSource})
		}
	}
	return inputs, nil
}

// 대소문자를 무시하고 견줄 때는 브라우저와 **같은 방식** 으로 낮춰야 한다.
// strings.ToLower 는 글자 하나씩만 보는 단순 변환이라 자바스크립트의
// toLowerCase 와 두 군데에서 갈린다.
//
//	ΟΔΟΣ  → strings.ToLower 는 οδοσ, 브라우저는 οδος (낱말 끝 시그마)
//	İ      → strings.ToLower 는 i,    브라우저는 i + 점(U+0307)
//
// 정렬은 화면이 먼저 그리고 서버가 덮으므로, 갈리는 만큼 줄이 튄다.
// x/text 의 cases.Lower 는 브라우저와 같은 유니코드 규칙을 따른다.
//
// 남는 차이는 유니코드 판 차이뿐이다. 크롬이 아는 새 글자를 x/text 가 아직
// 모르면 그 글자에서만 갈리고, x/text 를 올리면 함께 사라진다.
func newCaseFolder() cases.Caser { return cases.Lower(language.Und) }

func sortableValue(cell Cell, caseSensitive bool, folder cases.Caser) (sortScalar, error) {
	if len(bytes.TrimSpace(cell.Value)) == 0 || bytes.Equal(bytes.TrimSpace(cell.Value), []byte("null")) {
		return sortScalar{rank: 4, blank: true}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(cell.Value))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return sortScalar{}, fmt.Errorf("%w: cell %s has invalid JSON value", ErrInvalid, cellrange.Address(cell.Row, cell.Column))
	}
	switch typed := value.(type) {
	case json.Number:
		number, err := strconv.ParseFloat(string(typed), 64)
		if err != nil {
			return sortScalar{}, fmt.Errorf("%w: cell %s has an invalid number", ErrInvalid, cellrange.Address(cell.Row, cell.Column))
		}
		return sortScalar{rank: 0, number: number}, nil
	case string:
		if !caseSensitive {
			typed = folder.String(typed)
		}
		return sortScalar{rank: 1, text: typed}, nil
	case bool:
		return sortScalar{rank: 2, truth: typed}, nil
	case nil:
		return sortScalar{rank: 4, blank: true}, nil
	default:
		encoded, _ := json.Marshal(typed)
		return sortScalar{rank: 3, text: string(encoded)}, nil
	}
}

func compareSortScalars(left, right sortScalar, literal bool) int {
	if left.blank != right.blank {
		if left.blank {
			return 1
		}
		return -1
	}
	if left.rank != right.rank {
		if left.rank < right.rank {
			return -1
		}
		return 1
	}
	switch left.rank {
	case 0:
		if left.number < right.number {
			return -1
		}
		if left.number > right.number {
			return 1
		}
	case 2:
		if left.truth != right.truth {
			if !left.truth {
				return -1
			}
			return 1
		}
	default:
		if literal {
			return strings.Compare(left.text, right.text)
		}
		return compareNatural(left.text, right.text)
	}
	return 0
}
