package workbook

import (
	"context"

	"kanpic/internal/formula"
)

// ExternalFetcher answers WEBSERVICE and IMPORTDATA under the administrator's
// policy. The repository never opens a connection itself; it asks whatever was
// installed, and with nothing installed every call is refused with a reason.
type ExternalFetcher interface {
	Resolve(ctx context.Context, requests []formula.ExternalRequest) map[string]formula.ExternalResult
}

// collectExternalRequests lists every literal WEBSERVICE and IMPORTDATA call
// in the workbook's formulas and the write being applied, once each.
func collectExternalRequests(cells map[string]map[cellKey]Cell, submitted []CellInput) []formula.ExternalRequest {
	seen := make(map[string]struct{})
	requests := make([]formula.ExternalRequest, 0, 2)
	add := func(text string) {
		if text == "" {
			return
		}
		for _, request := range formula.ExternalRequests(text) {
			key := formula.ExternalKey(request.Function, request.URL)
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

func resolveExternalRequests(ctx context.Context, fetcher ExternalFetcher, requests []formula.ExternalRequest) map[string]formula.ExternalResult {
	if len(requests) == 0 {
		return nil
	}
	if fetcher == nil {
		results := make(map[string]formula.ExternalResult, len(requests))
		for _, request := range requests {
			results[formula.ExternalKey(request.Function, request.URL)] = formula.ExternalResult{Err: &formula.Error{Code: "#N/A", Message: "이 서버에는 외부 호출이 설치되어 있지 않습니다"}}
		}
		return results
	}
	return fetcher.Resolve(ctx, requests)
}

func (r *MemoryRepository) SetExternalFetcher(fetcher ExternalFetcher) { r.external = fetcher }

func (r *PostgresRepository) SetExternalFetcher(fetcher ExternalFetcher) { r.external = fetcher }

func (r *MemoryRepository) externalForLocked(cells map[string]map[cellKey]Cell, submitted []CellInput) map[string]formula.ExternalResult {
	return resolveExternalRequests(context.Background(), r.external, collectExternalRequests(cells, submitted))
}

func (r *PostgresRepository) externalFor(ctx context.Context, cells map[string]map[cellKey]Cell, submitted []CellInput) map[string]formula.ExternalResult {
	return resolveExternalRequests(ctx, r.external, collectExternalRequests(cells, submitted))
}
