package formula

import (
	"strings"
)

// ExternalResult is one resolved WEBSERVICE or IMPORTDATA call, fetched by the
// workbook layer before evaluation. The formula engine never opens a network
// connection itself: it reads what was fetched for it, which keeps evaluation
// synchronous and keeps the policy — which hosts, how large, how long — in one
// place that the administrator controls.
type ExternalResult struct {
	// Text is the response body for WEBSERVICE.
	Text string
	// Rows, Columns and Values are the parsed table for IMPORTDATA.
	Rows    int
	Columns int
	Values  []any
	Err     *Error
}

// ExternalRequest is one WEBSERVICE or IMPORTDATA call found in formula text.
type ExternalRequest struct {
	Function string
	URL      string
}

// ExternalKey normalizes a URL into the lookup key the workbook layer fills in
// and the parser reads back. Only surrounding whitespace is dropped: two URLs
// that differ in case may be two different resources.
func ExternalKey(function, url string) string {
	return strings.ToUpper(strings.TrimSpace(function)) + "\x00" + strings.TrimSpace(url)
}

// ExternalRequests lists the WEBSERVICE and IMPORTDATA calls a formula makes
// with a literal URL. A URL built from other cells cannot be fetched before
// evaluation, and the parser reports it.
func ExternalRequests(input string) []ExternalRequest {
	upper := strings.ToUpper(input)
	if !strings.Contains(upper, "WEBSERVICE") && !strings.Contains(upper, "IMPORTDATA") {
		return nil
	}
	tokens, err := lex(input)
	if err != nil {
		return nil
	}
	requests := make([]ExternalRequest, 0, 2)
	for index := 0; index+3 < len(tokens); index++ {
		if tokens[index].kind != tokenIdentifier {
			continue
		}
		name := strings.ToUpper(tokens[index].text)
		if name != "WEBSERVICE" && name != "IMPORTDATA" {
			continue
		}
		if tokens[index+1].kind != tokenLeft || tokens[index+2].kind != tokenString || tokens[index+3].kind != tokenRight {
			continue
		}
		requests = append(requests, ExternalRequest{Function: name, URL: tokens[index+2].text})
	}
	if len(requests) == 0 {
		return nil
	}
	return requests
}

// WithExternal supplies the responses WEBSERVICE and IMPORTDATA asked for,
// fetched and policy-checked before evaluation.
func (e *Evaluator) WithExternal(results map[string]ExternalResult) *Evaluator {
	e.scope.External = results
	return e
}

// evaluateExternal resolves WEBSERVICE and IMPORTDATA at parse time from what
// the workbook layer fetched. Anything it cannot resolve becomes an error that
// names the reason: a blank result reads as "no data" when the truth is
// "not allowed" or "not fetched".
func (p *parser) evaluateExternal(name string, arguments []node) (node, error) {
	if len(arguments) != 1 {
		return nil, argError(name)
	}
	address, literal := arguments[0].(literalNode)
	if !literal {
		return nil, formulaError("#VALUE!", name+"는 주소를 큰따옴표 안에 직접 적어야 합니다")
	}
	result, found := p.scope.External[ExternalKey(name, display(address.value))]
	if !found {
		return nil, formulaError("#N/A", "외부 호출이 꺼져 있거나 이 주소는 허용되지 않았습니다. 관리자 설정을 확인해 주세요")
	}
	if result.Err != nil {
		return nil, formulaError(result.Err.Code, result.Err.Message)
	}
	if name == "WEBSERVICE" {
		return literalNode{result.Text}, nil
	}
	if result.Rows == 0 || result.Columns == 0 {
		return literalNode{""}, nil
	}
	if result.Rows == 1 && result.Columns == 1 {
		return literalNode{result.Values[0]}, nil
	}
	return literalNode{arrayValue{rows: result.Rows, columns: result.Columns, values: result.Values}}, nil
}
