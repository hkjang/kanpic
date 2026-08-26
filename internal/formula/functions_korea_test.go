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

// 금액을 한글로 적는 일은 품의서와 세금계산서에 필수인데, 엑셀에서는
// 매크로를 짜야 한다. 틀린 값이 조용히 나가면 문서가 틀리므로 규칙을
// 촘촘히 못 박는다.
func TestHangulAmounts(t *testing.T) {
	t.Parallel()
	evaluator := New()
	for formula, expected := range map[string]any{
		`=HANGULNUM(0)`: "영",
		`=HANGULNUM(1)`: "일",
		// 십·백·천 앞의 일은 뺀다. 15 는 십오지 일십오가 아니다.
		`=HANGULNUM(15)`:   "십오",
		`=HANGULNUM(100)`:  "백",
		`=HANGULNUM(1000)`: "천",
		`=HANGULNUM(1004)`: "천사",
		`=HANGULNUM(1234)`: "천이백삼십사",
		// 만·억·조 앞에서는 남긴다. 10000 은 만이 아니라 일만이다.
		`=HANGULNUM(10000)`:      "일만",
		`=HANGULNUM(20000)`:      "이만",
		`=HANGULNUM(12345)`:      "일만이천삼백사십오",
		`=HANGULNUM(100000000)`:  "일억",
		`=HANGULNUM(1234567890)`: "십이억삼천사백오십육만칠천팔백구십",
		`=HANGULNUM(3200000)`:    "삼백이십만",
		// 가운데 묶음이 통째로 비면 건너뛴다.
		`=HANGULNUM(100000001)`: "일억일",
		`=HANGULWON(3200000)`:   "일금 삼백이십만원정",
		`=HANGULWON(0)`:         "일금 영원정",
		// 환불처럼 음수가 섞인 열도 있다. 오류를 내면 열 전체가 멈춘다.
		`=HANGULNUM(-1234)`: "마이너스 천이백삼십사",
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
	// 원 단위 아래는 한글로 적을 자리가 없다. 조용히 버리면 적힌 금액과
	// 셈한 금액이 달라지므로, 반올림은 사람이 뜻을 가지고 하게 둔다.
	if result := evaluator.Evaluate(`=HANGULWON(1234.5)`, nil); result.Error == nil || result.Error.Code != "#VALUE!" {
		t.Errorf("소수=%#v", result.Error)
	}
	// 표 프로그램의 숫자는 배정도 실수라 그 너머는 정수를 그대로 담지 못한다.
	// 담지 못한 값을 한글로 적으면 문서에 틀린 금액이 조용히 적힌다.
	if result := evaluator.Evaluate(`=HANGULWON(90071992547409920)`, nil); result.Error == nil || result.Error.Code != "#NUM!" {
		t.Errorf("너무 큰 금액=%#v", result.Error)
	}
	// 담을 수 있는 가장 큰 값은 적어야 한다.
	if result := evaluator.Evaluate(`=HANGULNUM(9007199254740992)`, nil); result.Error != nil {
		t.Errorf("가장 큰 금액=%v", result.Error)
	}
}
