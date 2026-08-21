package formula

import (
	"strings"
)

// MaxImportedCells bounds one IMPORTRANGE. A cross-workbook read is a copy, so
// the ceiling is deliberately lower than an in-workbook range.
const MaxImportedCells = 20_000

// ImportedRange is one resolved IMPORTRANGE, supplied by the workbook layer
// before evaluation. The formula engine never reaches across workbooks itself:
// it reads what was fetched for it, which keeps evaluation synchronous and
// keeps the permission check in one place.
type ImportedRange struct {
	Rows    int
	Columns int
	Values  []any
	Err     *Error
}

// ImportKey normalizes the two IMPORTRANGE arguments into the lookup key the
// workbook layer fills in and the parser reads back.
func ImportKey(source, area string) string {
	return strings.ToUpper(strings.TrimSpace(source)) + "\x00" + strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(area), "$", ""))
}

// ImportRequest is one IMPORTRANGE call found in formula text.
type ImportRequest struct {
	Source string
	Range  string
}

// ImportRequests lists the IMPORTRANGE calls a formula makes with literal
// arguments, which is what the workbook layer has to fetch before it can
// calculate. Calls whose arguments are not literal text are skipped: they
// cannot be resolved before evaluation and the parser reports them.
func ImportRequests(input string) []ImportRequest {
	if !strings.Contains(strings.ToUpper(input), "IMPORTRANGE") {
		return nil
	}
	tokens, err := lex(input)
	if err != nil {
		return nil
	}
	requests := make([]ImportRequest, 0, 2)
	for index := 0; index+5 < len(tokens); index++ {
		if tokens[index].kind != tokenIdentifier || !strings.EqualFold(tokens[index].text, "IMPORTRANGE") {
			continue
		}
		if tokens[index+1].kind != tokenLeft || tokens[index+2].kind != tokenString ||
			tokens[index+3].kind != tokenComma || tokens[index+4].kind != tokenString || tokens[index+5].kind != tokenRight {
			continue
		}
		requests = append(requests, ImportRequest{Source: tokens[index+2].text, Range: tokens[index+4].text})
	}
	if len(requests) == 0 {
		return nil
	}
	return requests
}

// evaluateImportRange resolves IMPORTRANGE at parse time from the ranges the
// workbook layer fetched. Anything it cannot resolve becomes a formula error
// that names the reason, because a silent empty result reads as "no data" and
// a missing permission reads the same way.
func (p *parser) evaluateImportRange(arguments []node) (node, error) {
	if len(arguments) != 2 {
		return nil, argError("IMPORTRANGE")
	}
	source, sourceLiteral := arguments[0].(literalNode)
	area, areaLiteral := arguments[1].(literalNode)
	if !sourceLiteral || !areaLiteral {
		return nil, formulaError("#VALUE!", "IMPORTRANGE는 워크북 주소와 범위를 큰따옴표 안에 직접 적어야 합니다")
	}
	imported, found := p.scope.Imports[ImportKey(display(source.value), display(area.value))]
	if !found {
		return nil, formulaError("#REF!", "가져올 수 없는 원본입니다. 워크북 주소와 범위를 확인해 주세요")
	}
	if imported.Err != nil {
		return nil, imported.Err
	}
	if imported.Rows == 1 && imported.Columns == 1 {
		return literalNode{value: imported.Values[0]}, nil
	}
	return importedNode{imported: imported}, nil
}

// importedNode holds the fetched block. It carries no dependencies: the cells
// it mirrors live in another workbook, so refreshing is driven by IMPORTRANGE
// being volatile rather than by the dependency graph.
type importedNode struct{ imported ImportedRange }

func (n importedNode) eval(map[string]any) (any, error) {
	return arrayValue{rows: n.imported.Rows, columns: n.imported.Columns, values: n.imported.Values}, nil
}
