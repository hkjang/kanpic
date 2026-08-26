package formula

import "testing"

// 사업자등록번호와 법인등록번호는 검사 숫자를 가지고 있다. 손으로 확인하던
// 것을 수식으로 옮기면, 명단을 한 번에 훑을 수 있다.
func TestKoreanIdentifierChecksums(t *testing.T) {
	t.Parallel()
	evaluator := New()
	for formula, expected := range map[string]any{
		// 하이픈은 있어도 없어도 같다. 사람이 적는 대로 받는다.
		`=ISBIZNO("220-81-62517")`: true,
		`=ISBIZNO("2208162517")`:   true,
		`=ISBIZNO("124-81-00998")`: true,
		// 마지막 한 자리만 달라도 걸러야 한다. 그것이 검사 숫자를 두는 까닭이다.
		`=ISBIZNO("220-81-62518")`: false,
		// 자릿수가 다르면 번호가 아니다.
		`=ISBIZNO("22081625")`:         false,
		`=ISBIZNO("")`:                 false,
		`=ISCORPNO("110111-0000002")`:  true,
		`=ISCORPNO("110111-0000003")`:  false,
		`=FORMATBIZNO("2208162517")`:   "220-81-62517",
		`=FORMATBIZNO("220-81-62517")`: "220-81-62517",
	} {
		result := evaluator.Evaluate(formula, nil)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if result.Value != expected {
			t.Errorf("%s = %v, want %v", formula, result.Value, expected)
		}
	}
	// 열 자리가 아니면 꼴을 갖출 수 없다. 어림잡아 적으면 틀린 번호가 된다.
	if result := evaluator.Evaluate(`=FORMATBIZNO("1234")`, nil); result.Error == nil || result.Error.Code != "#VALUE!" {
		t.Errorf("짧은 번호=%#v", result.Error)
	}
}

// 주민등록번호는 가리기만 한다. 검증은 하지 않는다.
//
// 2020년 10월부터 새로 매기는 번호는 뒷자리를 임의로 만든다. 검사 숫자가
// 맞지 않아도 진짜 번호다. 검증 함수를 두면 멀쩡한 번호를 틀렸다고 말하게
// 되는데, 그것은 없느니만 못하다.
func TestResidentNumberIsMaskedNotValidated(t *testing.T) {
	t.Parallel()
	evaluator := New()
	for formula, expected := range map[string]any{
		`=MASKRRN("900101-1234567")`:   "900101-1******",
		`=MASKRRN("9001011234567")`:    "900101-1******",
		`=MASKRRN("900101-1234567",0)`: "900101-*******",
		`=MASKRRN("900101-1234567",3)`: "900101-123****",
	} {
		result := evaluator.Evaluate(formula, nil)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if result.Value != expected {
			t.Errorf("%s = %v, want %v", formula, result.Value, expected)
		}
	}
	// 가릴 수 없는 것을 그대로 돌려주면, 가린 줄 알았던 자리에 번호가 그대로
	// 남는다. 가리는 함수는 못 가렸다고 말해야 한다.
	for _, formula := range []string{`=MASKRRN("900101-123456")`, `=MASKRRN("")`, `=MASKRRN("아무개")`} {
		if result := evaluator.Evaluate(formula, nil); result.Error == nil {
			t.Errorf("%s 가 %v 를 돌려줬다. 못 가렸으면 그렇게 말해야 한다", formula, result.Value)
		}
	}
	// 일곱 자리를 넘겨 남기라고 하면 가리는 뜻이 없어진다.
	if result := evaluator.Evaluate(`=MASKRRN("900101-1234567",8)`, nil); result.Error == nil {
		t.Errorf("여덟 자리를 남겼다: %v", result.Value)
	}
	// 검증 함수는 두지 않는다. 두면 2020년 이후 번호를 틀렸다고 말한다.
	if IsBuiltInFunction("ISRRN") {
		t.Error("주민등록번호 검증 함수가 생겼다 — 2020년 이후 번호는 검사 숫자가 맞지 않는다")
	}
}
