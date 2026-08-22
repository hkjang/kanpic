package formula

import (
	"fmt"
	"sort"
	"strings"
)

const (
	MaxGraphFormulas = 100_000
	MaxGraphEdges    = 1_000_000
)

type CellState struct {
	Value       any
	Formula     string
	ForcedError *Error
}

type RecalculatedCell struct {
	Address      string   `json:"address"`
	Value        any      `json:"value,omitempty"`
	Dependencies []string `json:"dependencies"`
	Error        *Error   `json:"error,omitempty"`
	Circular     bool     `json:"circular"`
}

type GraphResult struct {
	Cells  []RecalculatedCell `json:"cells"`
	Cycles []string           `json:"cycles"`
}

// Recalculate builds a dependency graph from the authoritative sheet state and
// evaluates only formula cells reachable from the changed addresses. The graph
// is intentionally stateless so every server instance reaches the same result.
func (e *Evaluator) Recalculate(cells map[string]CellState, changed []string) (GraphResult, error) {
	normalized := make(map[string]CellState, len(cells))
	values := make(map[string]any, len(cells))
	for address, cell := range cells {
		address = normalizeAddress(address)
		if _, _, valid := SplitCellKey(address); !valid {
			return GraphResult{}, fmt.Errorf("invalid cell address %q", address)
		}
		normalized[address] = cell
		values[address] = cell.Value
		if cell.Formula != "" {
			if code, ok := cell.Value.(string); ok && isFormulaErrorCode(code) {
				values[address] = formulaError(code, "stored formula error")
			}
		}
		if cell.ForcedError != nil {
			values[address] = cell.ForcedError
		}
	}

	if len(normalized) > 0 && len(changed) == 0 {
		return GraphResult{}, nil
	}

	// Unbounded references reach as far as the sheet's content. The addresses
	// being changed count too: clearing the last row of a table must still
	// reach the SUM(A:A) that used to include it.
	scoped := *e
	scoped.scope.Extent = measureExtent(keysOf(values), changed)
	e = &scoped

	nodes := make(map[string]node)
	dependencies := make(map[string][]string)
	parseErrors := make(map[string]*Error)
	reverse := make(map[string][]string)
	edges := 0
	for address, cell := range normalized {
		if strings.TrimSpace(cell.Formula) == "" {
			continue
		}
		if len(nodes)+len(parseErrors) >= MaxGraphFormulas {
			return GraphResult{}, fmt.Errorf("formula graph exceeds %d formulas", MaxGraphFormulas)
		}
		currentSheet, _, _ := SplitCellKey(address)
		parser, err := e.newParser(cell.Formula, currentSheet, address)
		if err != nil {
			parseErrors[address] = formulaError("#ERROR!", err.Error())
			continue
		}
		root, err := parser.parse()
		deps := make([]string, 0, len(parser.dependencies))
		for dependency := range parser.dependencies {
			deps = append(deps, dependency)
			reverse[dependency] = append(reverse[dependency], address)
			edges++
			if edges > MaxGraphEdges {
				return GraphResult{}, fmt.Errorf("formula graph exceeds %d dependency edges", MaxGraphEdges)
			}
		}
		sort.Strings(deps)
		dependencies[address] = deps
		if err != nil {
			if typed, ok := err.(*Error); ok {
				parseErrors[address] = typed
			} else {
				parseErrors[address] = formulaError("#ERROR!", err.Error())
			}
			continue
		}
		nodes[address] = root
	}
	for dependency := range reverse {
		sort.Strings(reverse[dependency])
	}

	affected := make(map[string]bool)
	queued := make(map[string]bool)
	queue := make([]string, 0, len(changed))
	for _, address := range changed {
		address = normalizeAddress(address)
		if _, _, valid := SplitCellKey(address); !valid {
			return GraphResult{}, fmt.Errorf("invalid changed cell address %q", address)
		}
		if !queued[address] {
			queue = append(queue, address)
			queued[address] = true
		}
		if _, formula := dependencies[address]; formula {
			affected[address] = true
		}
		if _, invalidFormula := parseErrors[address]; invalidFormula {
			affected[address] = true
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dependent := range reverse[current] {
			affected[dependent] = true
			if !queued[dependent] {
				queued[dependent] = true
				queue = append(queue, dependent)
			}
		}
	}
	if len(affected) > MaxGraphFormulas {
		return GraphResult{}, fmt.Errorf("recalculation exceeds %d formulas", MaxGraphFormulas)
	}

	const (
		unvisited = iota
		visiting
		done
	)
	states := make(map[string]int, len(affected))
	stack := make([]string, 0)
	cycles := make(map[string]bool)
	results := make(map[string]RecalculatedCell, len(affected))

	var calculate func(string) *Error
	calculate = func(address string) *Error {
		if states[address] == done {
			if formulaErr, ok := values[address].(*Error); ok {
				return formulaErr
			}
			return nil
		}
		if states[address] == visiting {
			start := 0
			for index, candidate := range stack {
				if candidate == address {
					start = index
					break
				}
			}
			for _, member := range stack[start:] {
				cycles[member] = true
				cycleErr := formulaError("#CIRC!", "circular reference")
				values[member] = cycleErr
				results[member] = RecalculatedCell{Address: member, Dependencies: dependencies[member], Error: cycleErr, Circular: true}
			}
			return formulaError("#CIRC!", "circular reference")
		}

		states[address] = visiting
		stack = append(stack, address)
		for _, dependency := range dependencies[address] {
			if affected[dependency] && (nodes[dependency] != nil || parseErrors[dependency] != nil) {
				calculate(dependency)
			}
		}

		if !cycles[address] {
			if parseErr := parseErrors[address]; parseErr != nil {
				values[address] = parseErr
				results[address] = RecalculatedCell{Address: address, Dependencies: dependencies[address], Error: parseErr}
			} else if forced := normalized[address].ForcedError; forced != nil {
				values[address] = forced
				results[address] = RecalculatedCell{Address: address, Dependencies: dependencies[address], Error: forced}
			} else if root := nodes[address]; root != nil {
				value, err := root.eval(values)
				if err != nil {
					formulaErr, ok := err.(*Error)
					if !ok {
						formulaErr = formulaError("#ERROR!", err.Error())
					}
					values[address] = formulaErr
					results[address] = RecalculatedCell{Address: address, Dependencies: dependencies[address], Error: formulaErr}
				} else {
					value = publicValue(value)
					values[address] = value
					results[address] = RecalculatedCell{Address: address, Value: value, Dependencies: dependencies[address]}
				}
			}
		}
		stack = stack[:len(stack)-1]
		states[address] = done
		if formulaErr, ok := values[address].(*Error); ok {
			return formulaErr
		}
		return nil
	}

	addresses := make([]string, 0, len(affected))
	for address := range affected {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		calculate(address)
	}

	result := GraphResult{Cells: make([]RecalculatedCell, 0, len(results)), Cycles: make([]string, 0, len(cycles))}
	for _, address := range addresses {
		if cell, ok := results[address]; ok {
			result.Cells = append(result.Cells, cell)
		}
	}
	for address := range cycles {
		result.Cycles = append(result.Cycles, address)
	}
	sort.Strings(result.Cycles)
	return result, nil
}

func normalizeAddress(address string) string {
	sheet, cell, valid := SplitCellKey(address)
	if !valid {
		return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(address), "$", ""))
	}
	return CellKey(sheet, cell)
}

// ErrorCodes is every error a formula can end in. It is one list because two
// lists drift: the tokenizer and this check disagreed about nothing so far
// only by luck. The product shows a Korean explanation for each of these, so
// adding one here means adding one there.
var ErrorCodes = []string{"#CIRC!", "#DIV/0!", "#ERROR!", "#N/A", "#NAME?", "#NULL!", "#NUM!", "#REF!", "#SPILL!", "#VALUE!"}

func isFormulaErrorCode(code string) bool {
	for _, known := range ErrorCodes {
		if known == code {
			return true
		}
	}
	return false
}
