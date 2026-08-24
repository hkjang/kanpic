package formula

import "testing"

// 채권 함수는 모두 "결제일이 어느 이자 기간 안에 있고, 그 기간이 며칠이며,
// 앞뒤로 며칠 떨어져 있는가" 에서 시작한다. 이 셈은 손으로 검산할 수 있다.
func TestCouponDatesAndDayCounts(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{`=COUPPCD("2018-01-25","2018-11-15",2)`, "2017-11-15"},
		{`=COUPNCD("2018-01-25","2018-11-15",2)`, "2018-05-15"},
		{`=COUPNUM("2018-01-25","2018-11-15",2)`, "2"},
		{`=COUPNUM("2018-01-25","2020-11-15",4)`, "12"},
		// 30/360 에서는 실제 날짜와 상관없이 360 을 횟수로 나눈다.
		{`=COUPDAYS("2018-01-25","2018-11-15",2)`, "180"},
		{`=COUPDAYBS("2018-01-25","2018-11-15",2)`, "70"},
		{`=COUPDAYSNC("2018-01-25","2018-11-15",2)`, "110"},
		// 실제/실제 기준에서는 지급일 사이의 진짜 날 수다.
		{`=COUPDAYS("2018-01-25","2018-11-15",2,1)`, "181"},
		{`=COUPDAYBS("2018-01-25","2018-11-15",2,1)`, "71"},
		{`=COUPDAYSNC("2018-01-25","2018-11-15",2,1)`, "110"},
		// 만기일이 그 달의 마지막 날이면 지급일도 모두 마지막 날이다.
		{`=COUPPCD("2018-03-05","2019-02-28",4)`, "2018-02-28"},
		{`=COUPNCD("2018-03-05","2019-02-28",4)`, "2018-05-31"},
		// 결제일이 지급일과 겹치면 그날이 지난 지급일이다.
		{`=COUPNCD("2018-05-15","2018-11-15",2)`, "2018-11-15"},
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
	// 30/360 과 실제/실제 기준에서는 지난 날과 남은 날을 더하면 기간
	// 길이다. 어느 하나가 어긋나면 경과이자가 조용히 틀어진다.
	//
	// 기준 2(실제/360)와 3(실제/365)은 **더해도 맞지 않는다.** 기간 길이는
	// 360이나 365를 횟수로 나눈 값인데 앞뒤 날 수는 진짜 날짜를 세기
	// 때문이다. 엑셀이 그렇게 정해 두었고, 채권 가격 공식도 그 조합을
	// 그대로 쓴다. 여기서 억지로 맞추면 가격이 엑셀과 달라진다.
	for _, basis := range []string{"0", "1", "4"} {
		sum := engine.Evaluate(`=COUPDAYBS("2018-01-25","2018-11-15",2,`+basis+`)+COUPDAYSNC("2018-01-25","2018-11-15",2,`+basis+`)`, map[string]any{})
		whole := engine.Evaluate(`=COUPDAYS("2018-01-25","2018-11-15",2,`+basis+`)`, map[string]any{})
		if sum.Error != nil || whole.Error != nil || display(sum.Value) != display(whole.Value) {
			t.Errorf("기준 %s: 지난 날+남은 날=%v, 기간=%v", basis, sum.Value, whole.Value)
		}
	}
	// 그 두 기준이 실제로 어긋나 있는지 적어 둔다. 나중에 "버그" 로 보고
	// 고치는 일이 없도록.
	for _, item := range []struct{ basis, period, parts string }{
		{"2", "180", "181"},
		{"3", "182.5", "181"},
	} {
		period := engine.Evaluate(`=COUPDAYS("2018-01-25","2018-11-15",2,`+item.basis+`)`, map[string]any{})
		parts := engine.Evaluate(`=COUPDAYBS("2018-01-25","2018-11-15",2,`+item.basis+`)+COUPDAYSNC("2018-01-25","2018-11-15",2,`+item.basis+`)`, map[string]any{})
		if display(period.Value) != item.period || display(parts.Value) != item.parts {
			t.Errorf("기준 %s: 기간=%v(기대 %s), 앞뒤 합=%v(기대 %s)", item.basis, period.Value, item.period, parts.Value, item.parts)
		}
	}
	for _, expression := range []string{
		`=COUPPCD("2018-11-15","2018-11-15",2)`, `=COUPDAYS("2018-01-25","2018-11-15",3)`,
		`=COUPDAYS("2018-01-25","2018-11-15",2,9)`,
	} {
		if result := engine.Evaluate(expression, map[string]any{}); result.Error == nil {
			t.Errorf("%s 가 %v 를 냈다", expression, result.Value)
		}
	}
}

// 가격과 수익률. 결제일이 지급일과 겹치는 깨끗한 경우는 표준 연금 공식과
// 같아야 하므로, 그 값을 파이썬으로 따로 계산해 맞췄다.
func TestBondPriceAndYield(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		// 만기 3년, 표면 6%, 수익률 8%, 반년마다. 연금 공식으로 94.7578631433.
		{`=ROUND(PRICE("2018-01-01","2021-01-01",0.06,0.08,100,2),10)`, "94.7578631433"},
		{`=ROUND(YIELD("2018-01-01","2021-01-01",0.06,94.7578631433,100,2),9)`, "0.08"},
		{`=ROUND(DURATION("2018-01-01","2048-01-01",0.08,0.09,2),7)`, "10.9530959"},
		{`=ROUND(MDURATION("2018-01-01","2048-01-01",0.08,0.09,2),7)`, "10.4814315"},
		// 만기에 한 번만 이자를 주는 증권: 1000×10%×(75/365).
		{`=ROUND(ACCRINTM("2018-04-01","2018-06-15",0.1,1000,3),10)`, "20.5479452055"},
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
	// 기간 가운데에서도 왕복해야 한다. 경과이자를 빼지 않으면 여기서
	// 어긋난다.
	roundTrip := `=ROUND(YIELD("2018-02-15","2027-11-15",0.0575,PRICE("2018-02-15","2027-11-15",0.0575,0.065,100,2),100,2),9)`
	if result := engine.Evaluate(roundTrip, map[string]any{}); result.Error != nil || display(result.Value) != "0.065" {
		t.Errorf("기간 가운데 왕복=%#v", result)
	}
	// 수정 듀레이션은 맥컬레이보다 언제나 작다.
	longer := engine.Evaluate(`=DURATION("2018-01-01","2048-01-01",0.08,0.09,2)`, map[string]any{})
	shorter := engine.Evaluate(`=MDURATION("2018-01-01","2048-01-01",0.08,0.09,2)`, map[string]any{})
	if longer.Error != nil || shorter.Error != nil {
		t.Fatalf("듀레이션=%#v %#v", longer, shorter)
	}
	if shorter.Value.(float64) >= longer.Value.(float64) {
		t.Errorf("수정 듀레이션이 맥컬레이보다 작지 않다: %v >= %v", shorter.Value, longer.Value)
	}
	for _, expression := range []string{
		`=PRICE("2018-01-01","2021-01-01",0.06,0.08,100,3)`,
		`=YIELD("2018-01-01","2021-01-01",0.06,0,100,2)`,
		`=PRICE("2021-01-01","2018-01-01",0.06,0.08,100,2)`,
		`=PRICE("2018-01-01","2021-01-01",-0.01,0.08,100,2)`,
		`=ACCRINTM("2018-06-15","2018-04-01",0.1,1000)`,
	} {
		if result := engine.Evaluate(expression, map[string]any{}); result.Error == nil {
			t.Errorf("%s 가 %v 를 냈다", expression, result.Value)
		}
	}
}
