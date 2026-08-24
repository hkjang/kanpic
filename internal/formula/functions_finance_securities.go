package formula

import (
	"math"
	"time"
)

// 채권과 단기 증권을 다루는 함수들. 모두 **날짜 사이의 연 단위 길이** 를
// 어떻게 세느냐에 달려 있고, 그 셈은 YEARFRAC 이 이미 하고 있다. 옮겨
// 적으면 기준(basis)을 한쪽만 늘리게 된다.
func evaluateSecurities(name string, values []any) (any, bool, error) {
	switch name {
	case "DISC", "INTRATE", "RECEIVED", "PRICEDISC", "YIELDDISC":
		return evaluateDiscountSecurity(name, values)
	case "PRICEMAT", "YIELDMAT":
		return evaluateMaturitySecurity(name, values)
	case "TBILLEQ", "TBILLPRICE", "TBILLYIELD":
		return evaluateTreasuryBill(name, values)
	case "DOLLARDE", "DOLLARFR":
		return evaluateDollarFraction(name, values)
	case "FVSCHEDULE", "ISPMT":
		return evaluateSimpleFinance(name, values)
	}
	return nil, false, nil
}

// securityDates 는 앞의 두 인수를 날짜로 읽고 기준을 정한다. 결제일이
// 만기일보다 뒤면 셈이 되지 않는다.
func securityDates(name string, values []any, basisIndex int) (time.Time, time.Time, int, error) {
	settlement, ok := parseTime(values[0])
	if !ok {
		return time.Time{}, time.Time{}, 0, formulaError("#VALUE!", name+" requires dates")
	}
	maturity, ok := parseTime(values[1])
	if !ok {
		return time.Time{}, time.Time{}, 0, formulaError("#VALUE!", name+" requires dates")
	}
	if !settlement.Before(maturity) {
		return time.Time{}, time.Time{}, 0, formulaError("#NUM!", name+" needs a settlement date before the maturity date")
	}
	basis := 0
	if basisIndex < len(values) && !omitted(values[basisIndex]) {
		number, numberOK := toNumber(values[basisIndex])
		if !numberOK || number < 0 || number > 4 || number != math.Trunc(number) {
			return time.Time{}, time.Time{}, 0, formulaError("#NUM!", name+" basis must be a whole number from 0 to 4")
		}
		basis = int(number)
	}
	return settlement, maturity, basis, nil
}

func evaluateDiscountSecurity(name string, values []any) (any, bool, error) {
	if len(values) < 4 || len(values) > 5 {
		return nil, true, argError(name)
	}
	settlement, maturity, basis, err := securityDates(name, values, 4)
	if err != nil {
		return nil, true, err
	}
	third, fourth, err := twoNumbers(name, values[2:4])
	if err != nil {
		return nil, true, err
	}
	span := yearFraction(settlement, maturity, basis)
	if span <= 0 {
		return nil, true, formulaError("#NUM!", name+" needs dates that are at least a day apart")
	}
	switch name {
	case "DISC":
		// 세 번째가 가격, 네 번째가 상환액이다.
		if third <= 0 || fourth <= 0 {
			return nil, true, formulaError("#NUM!", "DISC needs a positive price and redemption")
		}
		return (fourth - third) / fourth / span, true, nil
	case "INTRATE":
		if third <= 0 || fourth <= 0 {
			return nil, true, formulaError("#NUM!", "INTRATE needs a positive investment and redemption")
		}
		return (fourth - third) / third / span, true, nil
	case "YIELDDISC":
		if third <= 0 || fourth <= 0 {
			return nil, true, formulaError("#NUM!", "YIELDDISC needs a positive price and redemption")
		}
		return (fourth - third) / third / span, true, nil
	case "RECEIVED":
		// 세 번째가 투자액, 네 번째가 할인율이다.
		if third <= 0 || fourth <= 0 {
			return nil, true, formulaError("#NUM!", "RECEIVED needs a positive investment and discount")
		}
		divisor := 1 - fourth*span
		if divisor <= 0 {
			return nil, true, formulaError("#NUM!", "RECEIVED discount is too large for this term")
		}
		return third / divisor, true, nil
	case "PRICEDISC":
		if third <= 0 || fourth <= 0 {
			return nil, true, formulaError("#NUM!", "PRICEDISC needs a positive discount and redemption")
		}
		return fourth * (1 - third*span), true, nil
	}
	return nil, false, nil
}

func evaluateMaturitySecurity(name string, values []any) (any, bool, error) {
	if len(values) < 5 || len(values) > 6 {
		return nil, true, argError(name)
	}
	settlement, maturity, basis, err := securityDates(name, values, 5)
	if err != nil {
		return nil, true, err
	}
	issue, ok := parseTime(values[2])
	if !ok {
		return nil, true, formulaError("#VALUE!", name+" requires dates")
	}
	if !issue.Before(settlement) {
		return nil, true, formulaError("#NUM!", name+" needs an issue date before the settlement date")
	}
	rate, last, err := twoNumbers(name, values[3:5])
	if err != nil {
		return nil, true, err
	}
	if rate < 0 {
		return nil, true, formulaError("#NUM!", name+" needs a rate that is not negative")
	}
	// A 는 발행일부터 결제일까지, DSM 은 결제일부터 만기일까지, B 는
	// 발행일부터 만기일까지의 길이다.
	accrued := yearFraction(issue, settlement, basis)
	remaining := yearFraction(settlement, maturity, basis)
	total := yearFraction(issue, maturity, basis)
	if name == "PRICEMAT" {
		if last < 0 {
			return nil, true, formulaError("#NUM!", "PRICEMAT needs a yield that is not negative")
		}
		divisor := 1 + remaining*last
		if divisor == 0 {
			return nil, true, formulaError("#NUM!", "PRICEMAT cannot discount with this yield")
		}
		return (100+total*rate*100)/divisor - accrued*rate*100, true, nil
	}
	if last <= 0 {
		return nil, true, formulaError("#NUM!", "YIELDMAT needs a positive price")
	}
	divisor := last/100 + accrued*rate
	if divisor == 0 || remaining == 0 {
		return nil, true, formulaError("#NUM!", "YIELDMAT cannot solve with these values")
	}
	return ((1+total*rate)/divisor - 1) / remaining, true, nil
}

func evaluateTreasuryBill(name string, values []any) (any, bool, error) {
	if len(values) != 3 {
		return nil, true, argError(name)
	}
	settlement, maturity, _, err := securityDates(name, values, 3)
	if err != nil {
		return nil, true, err
	}
	// 단기 국채는 만기가 결제일에서 한 해를 넘지 않는다.
	elapsed := math.Round(maturity.Sub(settlement).Hours() / 24)
	if elapsed > 366 {
		return nil, true, formulaError("#NUM!", name+" needs a maturity within one year of the settlement date")
	}
	third, ok := toNumber(values[2])
	if !ok {
		return nil, true, formulaError("#VALUE!", name+" requires numbers")
	}
	switch name {
	case "TBILLPRICE":
		if third <= 0 {
			return nil, true, formulaError("#NUM!", "TBILLPRICE needs a positive discount")
		}
		price := 100 * (1 - third*elapsed/360)
		if price <= 0 {
			return nil, true, formulaError("#NUM!", "TBILLPRICE discount is too large for this term")
		}
		return price, true, nil
	case "TBILLYIELD":
		if third <= 0 {
			return nil, true, formulaError("#NUM!", "TBILLYIELD needs a positive price")
		}
		return (100 - third) / third * (360 / elapsed), true, nil
	case "TBILLEQ":
		if third <= 0 {
			return nil, true, formulaError("#NUM!", "TBILLEQ needs a positive discount")
		}
		divisor := 360 - third*elapsed
		if divisor <= 0 {
			return nil, true, formulaError("#NUM!", "TBILLEQ discount is too large for this term")
		}
		return 365 * third / divisor, true, nil
	}
	return nil, false, nil
}

func evaluateDollarFraction(name string, values []any) (any, bool, error) {
	if len(values) != 2 {
		return nil, true, argError(name)
	}
	amount, fraction, err := twoNumbers(name, values)
	if err != nil {
		return nil, true, err
	}
	fraction = math.Trunc(fraction)
	if fraction < 0 {
		return nil, true, formulaError("#NUM!", name+" needs a fraction that is not negative")
	}
	if fraction == 0 {
		return nil, true, formulaError("#DIV/0!", name+" cannot use a fraction of zero")
	}
	whole := math.Trunc(amount)
	rest := amount - whole
	// 소수점 뒤 자리를 분자로 읽으려면 분모의 자리 수만큼 밀어야 한다.
	// 16 이면 두 자리, 32 도 두 자리다.
	digits := math.Ceil(math.Log10(fraction))
	if digits <= 0 {
		digits = 1
	}
	scale := math.Pow(10, digits)
	if name == "DOLLARDE" {
		return whole + rest*scale/fraction, true, nil
	}
	return whole + rest*fraction/scale, true, nil
}

func evaluateSimpleFinance(name string, values []any) (any, bool, error) {
	switch name {
	case "FVSCHEDULE":
		if len(values) < 2 {
			return nil, true, argError(name)
		}
		principal, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "FVSCHEDULE requires numbers")
		}
		for _, rate := range numericValues(values[1:]) {
			principal *= 1 + rate
		}
		return principal, true, nil
	case "ISPMT":
		if len(values) != 4 {
			return nil, true, argError(name)
		}
		rate, period, err := twoNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		count, present, err := twoNumbers(name, values[2:4])
		if err != nil {
			return nil, true, err
		}
		if count == 0 {
			return nil, true, formulaError("#DIV/0!", "ISPMT needs at least one period")
		}
		return present * rate * (period/count - 1), true, nil
	}
	return nil, false, nil
}
