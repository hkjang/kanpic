package formula

import (
	"sort"
	"strings"
	"testing"
)

// 엑셀은 2007 이후에 생긴 함수를 파일 안에 _xlfn. 이 붙은 이름으로 적는다.
// 읽을 때 떼지 않으면 IFS·XLOOKUP·STDEV.P 처럼 이미 셀 줄 아는 함수까지
// #NAME? 이 나고, 쓸 때 붙이지 않으면 내보낸 파일이 엑셀에서 같은 꼴이 된다.
// 두 방향은 서로를 되돌려야 한다.
func TestExcelFunctionPrefixesRoundTrip(t *testing.T) {
	t.Parallel()
	engine := New()
	names := make([]string, 0, len(excelPrefixedFunctions))
	for name := range excelPrefixedFunctions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		// 목록에 우리가 셀 줄 모르는 이름이 섞이면 안 된다. 엉뚱한 이름에
		// 접두사를 붙여 내보내면 멀쩡하던 수식이 엑셀에서 깨진다.
		if result := engine.Evaluate("="+name+"()", map[string]any{}); result.Error != nil && result.Error.Code == "#NAME?" {
			t.Errorf("%s 는 접두사 목록에 있는데 엔진이 모르는 이름이다", name)
			continue
		}
		written := ForExcel("=" + name + "(1)")
		if !strings.HasPrefix(written, "="+excelPrefixedFunctions[name]) {
			t.Errorf("%s 를 내보낼 때 접두사가 붙지 않았다: %q", name, written)
			continue
		}
		// 그리고 다시 읽으면 원래 이름으로 돌아와야 한다.
		if back := canonicalFunctionName(excelPrefixedFunctions[name] + name); back != canonicalFunctionName(name) {
			t.Errorf("%s 를 다시 읽으면 %q 가 된다", name, back)
		}
	}
}

// 접두사를 붙이는 일은 글자를 다루는 일이라, 함수가 아닌 것에까지 손대기
// 쉽다. 사람이 쓴 글과 이미 붙어 있는 이름은 건드리면 안 된다.
func TestForExcelOnlyTouchesFunctionNames(t *testing.T) {
	t.Parallel()
	pairs := [][2]string{
		{`=IFS(A1>1,"큼",TRUE,"작음")`, `=_xlfn.IFS(A1>1,"큼",TRUE,"작음")`},
		{`=SUM(A1:A9)`, `=SUM(A1:A9)`},
		{`=SUM(XLOOKUP(1,A:A,B:B))`, `=SUM(_xlfn.XLOOKUP(1,A:A,B:B))`},
		{`=FILTER(A1:B9,B1:B9>1)`, `=_xlfn._xlws.FILTER(A1:B9,B1:B9>1)`},
		{`=_xlfn.IFS(A1>1,"큼")`, `=_xlfn.IFS(A1>1,"큼")`},
		{`=STDEV.P(A1:A9)`, `=_xlfn.STDEV.P(A1:A9)`},
		{`=STDEVP(A1:A9)`, `=STDEVP(A1:A9)`},
		{`="IFS(" & A1`, `="IFS(" & A1`},
		{`=CONCATENATE("보고서 UNIQUE(", A1, ")")`, `=CONCATENATE("보고서 UNIQUE(", A1, ")")`},
		{`="그는 ""IFS("" 라고 썼다" & IFS(A1>1,"큼")`, `="그는 ""IFS("" 라고 썼다" & _xlfn.IFS(A1>1,"큼")`},
		{`=A1+UNIQUE`, `=A1+UNIQUE`},
		{`=Sheet1!A1+IFS(A1>0,1)`, `=Sheet1!A1+_xlfn.IFS(A1>0,1)`},
		{`=ifs(A1>1,"큼")`, `=_xlfn.ifs(A1>1,"큼")`},
		{``, ``},
	}
	for _, pair := range pairs {
		if actual := ForExcel(pair[0]); actual != pair[1] {
			t.Errorf("ForExcel(%q)\n  =%q\n  기대=%q", pair[0], actual, pair[1])
		}
	}
}

// 접두사가 붙은 채로 들어온 수식은 우리 엔진이 그대로 셀 수 있어야 한다.
// 오늘 엑셀에서 만든 파일에는 거의 다 붙어 있다.
func TestPrefixedFormulasEvaluate(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{`=_xlfn.STDEV.P(2,4,4,4,5,5,7,9)`, "2"},
		{`=_xlfn.IFS(1>0,"a")`, "a"},
		{`=_xlfn.XLOOKUP(2,{1,2,3},{"a","b","c"})`, "b"},
		{`=_xlfn.TEXTJOIN(",",TRUE,"a","b")`, "a,b"},
		{`=_xlfn.LET(x,2,x*3)`, "6"},
		{`=_xlfn.SWITCH(2,1,"a",2,"b")`, "b"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s -> %s %s", item.expression, result.Error.Code, result.Error.Message)
			continue
		}
		if actual := display(result.Value); actual != item.expected {
			t.Errorf("%s=%s, 기대=%s", item.expression, actual, item.expected)
		}
	}
	// 접두사를 떼도 모르는 이름은 여전히 모른다.
	if result := engine.Evaluate("=_xlfn.NOSUCHFUNC(1)", map[string]any{}); result.Error == nil || result.Error.Code != "#NAME?" {
		t.Errorf("모르는 이름=%#v", result)
	}
}

// 두 방향은 서로를 되돌려야 한다. 한쪽만 고치면 내보낸 파일을 다시 가져올
// 때 이름이 어긋난다.
func TestForExcelAndFromExcelUndoEachOther(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		`=IFS(A1>1,"큼",TRUE,"작음")`,
		`=SUM(XLOOKUP(1,A:A,B:B))`,
		`=FILTER(A1:B9,B1:B9>1)`,
		`=STDEV.P(A1:A9)+STDEVP(A1:A9)`,
		`="IFS(" & A1`,
		`="그는 ""IFS("" 라고 썼다" & IFS(A1>1,"큼")`,
		`=LET(x,UNIQUE(A1:A9),SUM(x))`,
		`=A1+1`,
		``,
	} {
		if back := FromExcel(ForExcel(text)); back != text {
			t.Errorf("%q -> %q -> %q", text, ForExcel(text), back)
		}
	}
	// 이미 접두사가 붙어 들어온 것도 한 번만 뗀다.
	if actual := FromExcel(`=_xlfn._xlws.FILTER(A1:B9,B1:B9>1)`); actual != `=FILTER(A1:B9,B1:B9>1)` {
		t.Errorf("FromExcel=%q", actual)
	}
	// 두 번 내보내도 접두사가 두 번 붙지 않는다.
	once := ForExcel(`=IFS(A1>1,"큼")`)
	if twice := ForExcel(once); twice != once {
		t.Errorf("두 번 내보낸 결과=%q", twice)
	}
}

// 이름을 이루는 글자에 한글이 빠져 있었다. 함수 이름은 모두 영문이라 여태
// 드러나지 않았지만, LAMBDA 의 매개변수는 사람이 짓는 이름이라 이 나라에서는
// 대개 한글이다. 매출 을 이름으로 못 읽으면 본문에서 그 자리를 찾지 못해
// 선언만 바뀌고 부르는 자리는 그대로 남는다 — 짝이 어긋난 수식이 된다.
func TestBareNameRewriteHandlesHangulAndSheetNames(t *testing.T) {
	t.Parallel()
	upper := func(word string) string {
		if word == "매출" {
			return "_xlpm.매출"
		}
		return word
	}
	for input, expected := range map[string]string{
		"매출-원가":              "_xlpm.매출-원가",
		"SUM(매출)":            "SUM(_xlpm.매출)",
		`IF(매출>0,"매출","없음")`: `IF(_xlpm.매출>0,"매출","없음")`,
		"매출+'매출'!A1":         "_xlpm.매출+'매출'!A1",
		"매출(1)":              "매출(1)",
		"매출액+매출":             "매출액+_xlpm.매출",
	} {
		if actual := RewriteBareNames(input, upper); actual != expected {
			t.Errorf("%s -> %s, want %s", input, actual, expected)
		}
	}
	// 함수 이름 쪽 훑기가 같은 자리를 쓰므로, 한글이 이름이 되어도 예전과
	// 같아야 한다. 등록되지 않은 이름은 그대로 나간다.
	if actual := ForExcel("=마진율(A1,B1)+IFS(A1>0,1,TRUE,0)"); actual != "=마진율(A1,B1)+_xlfn.IFS(A1>0,1,TRUE,0)" {
		t.Errorf("ForExcel = %s", actual)
	}
	if actual := FromExcel("=마진율(A1,B1)+_xlfn.IFS(A1>0,1,TRUE,0)"); actual != "=마진율(A1,B1)+IFS(A1>0,1,TRUE,0)" {
		t.Errorf("FromExcel = %s", actual)
	}
}
