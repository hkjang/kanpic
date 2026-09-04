// Package external fetches what WEBSERVICE and IMPORTDATA ask for, under the
// administrator's policy. It is off until switched on, reaches only hosts on
// the allow-list over https, refuses private and link-local addresses even when
// DNS points a permitted name at them, follows no redirects, and stops reading
// at a size ceiling. A spreadsheet that can call out is a spreadsheet that can
// be pointed at the metadata service or the database next door; the policy is
// what makes the function safe to hand to every user.
package external

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"kanpic/internal/formula"
)

type settingsProvider interface {
	Values(context.Context) (map[string]any, error)
}

// Config is the administrator's policy, read from settings on every fetch so a
// change takes effect without a restart.
type Config struct {
	Enabled      bool
	AllowedHosts []string
	Timeout      time.Duration
	MaxBytes     int64
	CacheFor     time.Duration
}

const (
	defaultTimeout  = 10 * time.Second
	defaultMaxBytes = 1 << 20 // 1 MiB
	defaultCacheFor = 5 * time.Minute
	// MaxRequestsPerRecalculation bounds one recalculation's outbound calls.
	MaxRequestsPerRecalculation = 20
	maxCSVCells                 = formula.MaxImportedCells
	// maxCacheEntries bounds the answer cache. Nothing but a new answer ever
	// touches the map, so without a ceiling a workbook that fetches a fresh
	// address on every recalculation would grow it until the process died.
	maxCacheEntries = 512
	// maxMessageRunes trims a foreign error before it goes in a cell.
	maxMessageRunes = 160
)

type cached struct {
	result  formula.ExternalResult
	expires time.Time
}

// Fetcher resolves external requests. It is safe for concurrent use.
type Fetcher struct {
	settings settingsProvider
	logger   *slog.Logger
	now      func() time.Time
	// allowPrivate lets tests reach an httptest server on the loopback
	// interface. Production never sets it.
	allowPrivate bool
	// tlsClient lets tests trust an httptest certificate. Production leaves it
	// nil and builds a client under the policy above.
	tlsClient *http.Client
	mu        sync.Mutex
	cache     map[string]cached
}

func New(settings settingsProvider, logger *slog.Logger) *Fetcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Fetcher{settings: settings, logger: logger, now: func() time.Time { return time.Now() }, cache: make(map[string]cached)}
}

func (f *Fetcher) config(ctx context.Context) (Config, error) {
	config := Config{Timeout: defaultTimeout, MaxBytes: defaultMaxBytes, CacheFor: defaultCacheFor}
	if f.settings == nil {
		return config, nil
	}
	values, err := f.settings.Values(ctx)
	if err != nil {
		return config, err
	}
	if enabled, ok := values["external.enabled"].(bool); ok {
		config.Enabled = enabled
	}
	config.AllowedHosts = stringList(values["external.allowed_hosts"])
	config.Timeout = time.Duration(bounded(values["external.timeout_seconds"], int(defaultTimeout/time.Second), 1, 120)) * time.Second
	config.MaxBytes = int64(bounded(values["external.max_kb"], defaultMaxBytes/1024, 1, 20*1024)) * 1024
	config.CacheFor = time.Duration(bounded(values["external.cache_seconds"], int(defaultCacheFor/time.Second), 0, 86_400)) * time.Second
	return config, nil
}

// Resolve answers every request, never returning fewer entries than it was
// asked for: a request that is refused comes back with the reason attached.
func (f *Fetcher) Resolve(ctx context.Context, requests []formula.ExternalRequest) map[string]formula.ExternalResult {
	if len(requests) == 0 {
		return nil
	}
	results := make(map[string]formula.ExternalResult, len(requests))
	fail := func(request formula.ExternalRequest, code, message string) {
		results[formula.ExternalKey(request.Function, request.URL)] = formula.ExternalResult{Err: &formula.Error{Code: code, Message: message}}
	}
	config, err := f.config(ctx)
	if err != nil {
		for _, request := range requests {
			fail(request, "#N/A", "외부 호출 설정을 읽지 못했습니다")
		}
		return results
	}
	if !config.Enabled {
		for _, request := range requests {
			fail(request, "#N/A", "외부 호출이 꺼져 있습니다. 관리자가 external.enabled 를 켜야 합니다")
		}
		return results
	}
	if len(requests) > MaxRequestsPerRecalculation {
		for _, request := range requests {
			fail(request, "#N/A", fmt.Sprintf("한 번에 외부 호출은 %d개까지입니다", MaxRequestsPerRecalculation))
		}
		return results
	}
	for _, request := range requests {
		key := formula.ExternalKey(request.Function, request.URL)
		if _, done := results[key]; done {
			continue
		}
		results[key] = f.one(ctx, config, request)
	}
	return results
}

func (f *Fetcher) one(ctx context.Context, config Config, request formula.ExternalRequest) formula.ExternalResult {
	// The policy check reads the administrator's settings and reaches nothing,
	// so it stands outside the cache in both directions: a refusal is not kept,
	// and an answer kept earlier is not handed back once the address has left
	// the allow-list. Otherwise a fixed allowed_hosts would take up to
	// cache_seconds to be believed.
	target, err := CheckURL(request.URL, config.AllowedHosts)
	if err != nil {
		return formula.ExternalResult{Err: &formula.Error{Code: "#N/A", Message: err.Error()}}
	}
	key := formula.ExternalKey(request.Function, request.URL)
	f.mu.Lock()
	if hit, ok := f.cache[key]; ok && f.now().Before(hit.expires) {
		f.mu.Unlock()
		return hit.result
	}
	f.mu.Unlock()
	body, err := f.fetch(ctx, config, target)
	var result formula.ExternalResult
	if err != nil {
		result = formula.ExternalResult{Err: &formula.Error{Code: "#N/A", Message: err.Error()}}
	} else if request.Function == "IMPORTDATA" {
		result = parseCSV(body)
	} else {
		result = formula.ExternalResult{Text: body}
	}
	if config.CacheFor > 0 {
		f.store(key, result, f.now().Add(config.CacheFor))
	}
	return result
}

// store keeps one answer and takes out the rubbish while it is holding the
// lock: expired entries first, and if the map is still full, whichever live
// entry expires soonest.
func (f *Fetcher) store(key string, result formula.ExternalResult, expires time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, replacing := f.cache[key]; !replacing && len(f.cache) >= maxCacheEntries {
		now := f.now()
		for existing, entry := range f.cache {
			if !now.Before(entry.expires) {
				delete(f.cache, existing)
			}
		}
		if len(f.cache) >= maxCacheEntries {
			var oldest string
			var oldestAt time.Time
			for existing, entry := range f.cache {
				if oldestAt.IsZero() || entry.expires.Before(oldestAt) {
					oldest, oldestAt = existing, entry.expires
				}
			}
			delete(f.cache, oldest)
		}
	}
	f.cache[key] = cached{result: result, expires: expires}
}

// Policy errors are what the cell shows, so they say what the person can do.
var (
	errScheme   = errors.New("주소는 https:// 로 시작해야 합니다")
	errHost     = errors.New("허용되지 않은 호스트입니다. 관리자가 external.allowed_hosts 에 적어야 합니다")
	errPrivate  = errors.New("사설망이나 이 서버 자신을 가리키는 주소는 부를 수 없습니다")
	errRedirect = errors.New("다른 주소로 넘기는 응답은 따라가지 않습니다")
	errTooLarge = errors.New("응답이 허용된 크기를 넘습니다")
)

// CheckURL applies the static part of the policy: scheme and allow-list. The
// address check happens at dial time, against the resolved IP.
func CheckURL(raw string, allowed []string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return nil, errors.New("주소를 읽을 수 없습니다")
	}
	if parsed.Scheme != "https" {
		return nil, errScheme
	}
	if parsed.User != nil {
		return nil, errors.New("주소에 사용자 이름이나 비밀번호를 넣을 수 없습니다")
	}
	if !HostAllowed(parsed.Hostname(), allowed) {
		return nil, errHost
	}
	return parsed, nil
}

// HostAllowed matches a host against the allow-list. An entry names one host
// exactly; "*.example.com" names every host below example.com but not
// example.com itself. Case does not matter. An empty list allows nothing.
func HostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	for _, entry := range allowed {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "*.") {
			if strings.HasSuffix(host, entry[1:]) && host != entry[2:] {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}

// PublicAddress reports whether an IP may be dialed: not loopback, not private,
// not link-local, not multicast, not unspecified, and not the cloud metadata
// address, whichever name DNS hung on it.
func PublicAddress(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64/10 is carrier NAT, 169.254.169.254 the metadata service.
		if v4[0] == 100 && v4[1]&0xc0 == 64 {
			return false
		}
		if v4[0] == 0 || v4[0] == 240 || (v4[0] == 192 && v4[1] == 0 && v4[2] == 0) {
			return false
		}
	}
	return true
}

func (f *Fetcher) fetch(ctx context.Context, config Config, target *url.URL) (string, error) {
	dialer := &net.Dialer{Timeout: config.Timeout}
	transport := &http.Transport{
		Proxy: nil,
		// Resolve here, check every address, and dial the checked address
		// itself, so a name that changes between the check and the dial (DNS
		// rebinding) cannot slip a private address through.
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, splitErr
			}
			addresses, lookupErr := net.DefaultResolver.LookupIPAddr(ctx, host)
			if lookupErr != nil {
				return nil, lookupErr
			}
			var chosen net.IP
			for _, candidate := range addresses {
				if !f.allowPrivate && !PublicAddress(candidate.IP) {
					return nil, errPrivate
				}
				if chosen == nil {
					chosen = candidate.IP
				}
			}
			if chosen == nil {
				return nil, errors.New("주소를 찾을 수 없습니다")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(chosen.String(), port))
		},
		TLSHandshakeTimeout:   config.Timeout,
		ResponseHeaderTimeout: config.Timeout,
		DisableKeepAlives:     true,
	}
	client := &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect }}
	if f.tlsClient != nil {
		// Tests: keep the policy dialer and redirect rule, borrow only the trust store.
		if testTransport, ok := f.tlsClient.Transport.(*http.Transport); ok {
			transport.TLSClientConfig = testTransport.TLSClientConfig
		}
	}
	requestContext, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "kanpic-spreadsheet/1 (+WEBSERVICE)")
	request.Header.Set("Accept", "text/plain, text/csv, application/json;q=0.9, */*;q=0.5")
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, errRedirect) || strings.Contains(err.Error(), errRedirect.Error()) {
			return "", errRedirect
		}
		if strings.Contains(err.Error(), errPrivate.Error()) {
			return "", errPrivate
		}
		f.logger.Warn("external fetch failed", "url", target.String(), "error", err)
		return "", errors.New("주소에 닿지 못했습니다: " + short(err.Error()))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", fmt.Errorf("주소가 %d 로 답했습니다", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, config.MaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", errors.New("응답을 읽지 못했습니다")
	}
	if int64(len(data)) > config.MaxBytes {
		return "", fmt.Errorf("%w (%d KB)", errTooLarge, config.MaxBytes/1024)
	}
	return string(data), nil
}

// parseCSV turns a body into the table IMPORTDATA spills. Comma is assumed,
// tab is honoured when the first line has tabs and no commas. Numbers become
// numbers so =SUM over the result works; everything else stays text.
func parseCSV(body string) formula.ExternalResult {
	body = strings.TrimPrefix(body, "\uFEFF")
	reader := csv.NewReader(strings.NewReader(body))
	if first, _, _ := strings.Cut(body, "\n"); strings.Contains(first, "\t") && !strings.Contains(first, ",") {
		reader.Comma = '\t'
	}
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return formula.ExternalResult{Err: &formula.Error{Code: "#VALUE!", Message: "CSV 를 읽지 못했습니다: " + short(err.Error())}}
	}
	columns := 0
	for _, record := range records {
		if len(record) > columns {
			columns = len(record)
		}
	}
	if len(records) == 0 || columns == 0 {
		return formula.ExternalResult{}
	}
	if len(records)*columns > maxCSVCells {
		return formula.ExternalResult{Err: &formula.Error{Code: "#NUM!", Message: fmt.Sprintf("IMPORTDATA 는 최대 %d칸까지 가져옵니다", maxCSVCells)}}
	}
	values := make([]any, 0, len(records)*columns)
	for _, record := range records {
		for column := 0; column < columns; column++ {
			if column >= len(record) {
				values = append(values, nil)
				continue
			}
			// 수로 읽는 자는 수식 엔진의 것 하나뿐이다 — 스프레드시트가
			// 수라고 부르는 것이지 Go 가 수라고 부르는 것이 아니다.
			// 16진수도, NaN 도, 밑줄로 자리를 가른 것도 수가 아니다.
			text := strings.TrimSpace(record[column])
			if number, ok := formula.DecimalNumber(text); ok {
				values = append(values, number)
			} else {
				values = append(values, record[column])
			}
		}
	}
	return formula.ExternalResult{Rows: len(records), Columns: columns, Values: values}
}

func stringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case string:
		return strings.Split(typed, ",")
	}
	return nil
}

func bounded(value any, fallback, minimum, maximum int) int {
	number := fallback
	switch typed := value.(type) {
	case float64:
		number = int(typed)
	case int:
		number = typed
	}
	if number < minimum {
		return minimum
	}
	if number > maximum {
		return maximum
	}
	return number
}

// short trims a message that came from elsewhere. It counts letters, not
// bytes, so a Korean message comes back whole instead of ending in a half
// letter the terminal draws as a question mark.
func short(text string) string {
	if utf8.RuneCountInString(text) <= maxMessageRunes {
		return text
	}
	counted := 0
	for index := range text {
		if counted == maxMessageRunes {
			return text[:index] + "…"
		}
		counted++
	}
	return text
}
