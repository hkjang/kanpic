package formula

import (
	"math"
	"time"
)

// 이자 지급일 계산. 채권 함수는 모두 "결제일이 어느 이자 기간 안에 있고,
// 그 기간이 며칠이며, 앞뒤로 며칠 떨어져 있는가" 에서 시작한다.
//
// 지급일은 만기일에서 거꾸로 (12/횟수) 달씩 물러나며 잡는다. 만기일이 그
// 달의 마지막 날이면 지급일도 모두 마지막 날이다 — 2월을 지나며 날짜가
// 밀리면 기간 길이가 어긋난다.

func couponFrequency(name string, value any) (int, error) {
	number, ok := toNumber(value)
	if !ok {
		return 0, formulaError("#VALUE!", name+" requires numbers")
	}
	switch math.Trunc(number) {
	case 1, 2, 4:
		return int(math.Trunc(number)), nil
	}
	return 0, formulaError("#NUM!", name+" frequency must be 1, 2 or 4")
}

func endOfMonth(moment time.Time) bool {
	return moment.AddDate(0, 0, 1).Day() == 1
}

// shiftMonths 는 달을 옮기되 날짜가 넘치지 않게 잡는다. 3월 31일에서 한
// 달을 물러나면 2월 28일이지, 3월 3일이 아니다.
func shiftMonths(moment time.Time, months int, keepEndOfMonth bool) time.Time {
	year, month, day := moment.Date()
	target := time.Date(year, month, 1, 0, 0, 0, 0, moment.Location()).AddDate(0, months, 0)
	lastDay := target.AddDate(0, 1, -1).Day()
	if keepEndOfMonth || day > lastDay {
		day = lastDay
	}
	return time.Date(target.Year(), target.Month(), day, 0, 0, 0, 0, moment.Location())
}

// previousCoupon 은 결제일 이전(같은 날 제외)의 마지막 지급일이다.
func previousCoupon(settlement, maturity time.Time, frequency int) time.Time {
	step := 12 / frequency
	lastDayOfMonth := endOfMonth(maturity)
	current := maturity
	for current.After(settlement) {
		previous := shiftMonths(current, -step, lastDayOfMonth)
		if !previous.After(settlement) {
			return previous
		}
		current = previous
	}
	return current
}

func nextCoupon(settlement, maturity time.Time, frequency int) time.Time {
	return shiftMonths(previousCoupon(settlement, maturity, frequency), 12/frequency, endOfMonth(maturity))
}

func couponCount(settlement, maturity time.Time, frequency int) int {
	step := 12 / frequency
	lastDayOfMonth := endOfMonth(maturity)
	count := 0
	for current := maturity; current.After(settlement); current = shiftMonths(current, -step, lastDayOfMonth) {
		count++
	}
	return count
}

// couponDays 는 결제일이 든 이자 기간의 길이다. 30/360 기준에서는 실제
// 날짜와 상관없이 360 을 횟수로 나눈다.
func couponDays(settlement, maturity time.Time, frequency, basis int) float64 {
	switch basis {
	case 1:
		previous := previousCoupon(settlement, maturity, frequency)
		return math.Round(nextCoupon(settlement, maturity, frequency).Sub(previous).Hours() / 24)
	case 3:
		return 365 / float64(frequency)
	default:
		return 360 / float64(frequency)
	}
}

func couponDaysBefore(settlement, maturity time.Time, frequency, basis int) float64 {
	previous := previousCoupon(settlement, maturity, frequency)
	if basis == 1 || basis == 2 || basis == 3 {
		return math.Round(settlement.Sub(previous).Hours() / 24)
	}
	return math.Round(yearFraction(previous, settlement, basis) * 360)
}

func couponDaysAfter(settlement, maturity time.Time, frequency, basis int) float64 {
	next := nextCoupon(settlement, maturity, frequency)
	if basis == 1 || basis == 2 || basis == 3 {
		return math.Round(next.Sub(settlement).Hours() / 24)
	}
	return math.Round(yearFraction(settlement, next, basis) * 360)
}

func evaluateCoupon(name string, values []any) (any, bool, error) {
	switch name {
	case "COUPPCD", "COUPNCD", "COUPNUM", "COUPDAYS", "COUPDAYBS", "COUPDAYSNC":
	default:
		return nil, false, nil
	}
	if len(values) < 3 || len(values) > 4 {
		return nil, true, argError(name)
	}
	settlement, maturity, basis, err := securityDates(name, values, 3)
	if err != nil {
		return nil, true, err
	}
	frequency, err := couponFrequency(name, values[2])
	if err != nil {
		return nil, true, err
	}
	switch name {
	case "COUPPCD":
		return previousCoupon(settlement, maturity, frequency).Format("2006-01-02"), true, nil
	case "COUPNCD":
		return nextCoupon(settlement, maturity, frequency).Format("2006-01-02"), true, nil
	case "COUPNUM":
		return float64(couponCount(settlement, maturity, frequency)), true, nil
	case "COUPDAYS":
		return couponDays(settlement, maturity, frequency, basis), true, nil
	case "COUPDAYBS":
		return couponDaysBefore(settlement, maturity, frequency, basis), true, nil
	case "COUPDAYSNC":
		return couponDaysAfter(settlement, maturity, frequency, basis), true, nil
	}
	return nil, false, nil
}

// evaluateBond 는 이자를 여러 번 주는 채권을 다룬다. 위의 지급일 계산 위에
// 그대로 얹는다.
//
//	가격 = 상환액/(1+y)^(N-1+f) + Σ 이자/(1+y)^(k-1+f) − 이자×A/E
//
// 여기서 f 는 다음 지급일까지의 남은 비율, A 는 지난 지급일부터 결제일까지
// 지난 날 수다. 마지막 항이 **경과이자** 로, 사는 쪽이 파는 쪽에 따로 주는
// 몫이다. 빼지 않으면 가격이 그만큼 부풀어 오른다.
func evaluateBond(name string, values []any) (any, bool, error) {
	switch name {
	case "PRICE", "YIELD", "DURATION", "MDURATION", "ACCRINTM":
	default:
		return nil, false, nil
	}
	if name == "ACCRINTM" {
		return evaluateAccruedAtMaturity(values)
	}
	minimum := 5
	if name == "PRICE" || name == "YIELD" {
		minimum = 6
	}
	if len(values) < minimum || len(values) > minimum+1 {
		return nil, true, argError(name)
	}
	settlement, maturity, basis, err := securityDates(name, values, minimum)
	if err != nil {
		return nil, true, err
	}
	rate, third, err := twoNumbers(name, values[2:4])
	if err != nil {
		return nil, true, err
	}
	if rate < 0 {
		return nil, true, formulaError("#NUM!", name+" needs a coupon rate that is not negative")
	}
	frequencyIndex := 4
	redemption := 100.0
	if name == "PRICE" || name == "YIELD" {
		frequencyIndex = 5
		if redemption, err = singleNumber(name, values[4]); err != nil {
			return nil, true, err
		}
		if redemption <= 0 {
			return nil, true, formulaError("#NUM!", name+" needs a positive redemption value")
		}
	}
	frequency, err := couponFrequency(name, values[frequencyIndex])
	if err != nil {
		return nil, true, err
	}
	count := couponCount(settlement, maturity, frequency)
	if count < 1 {
		return nil, true, formulaError("#NUM!", name+" needs at least one coupon left")
	}
	periodDays := couponDays(settlement, maturity, frequency, basis)
	if periodDays <= 0 {
		return nil, true, formulaError("#NUM!", name+" cannot measure the coupon period")
	}
	elapsed := couponDaysBefore(settlement, maturity, frequency, basis)
	remaining := couponDaysAfter(settlement, maturity, frequency, basis)
	coupon := 100 * rate / float64(frequency)

	price := func(yield float64) float64 {
		periodYield := yield / float64(frequency)
		share := remaining / periodDays
		total := redemption / math.Pow(1+periodYield, float64(count)-1+share)
		for index := 1; index <= count; index++ {
			total += coupon / math.Pow(1+periodYield, float64(index)-1+share)
		}
		return total - coupon*elapsed/periodDays
	}

	switch name {
	case "PRICE":
		if third < 0 {
			return nil, true, formulaError("#NUM!", "PRICE needs a yield that is not negative")
		}
		return price(third), true, nil
	case "YIELD":
		if third <= 0 {
			return nil, true, formulaError("#NUM!", "YIELD needs a positive price")
		}
		// 가격은 수익률이 오르면 내려간다. 값이 줄어드는 함수를 되돌린다.
		answer, ok := solveDecreasing(third, 0.1, price)
		if !ok {
			return nil, true, formulaError("#NUM!", "YIELD could not find a rate for this price")
		}
		return answer, true, nil
	case "DURATION", "MDURATION":
		yield := third
		if yield < 0 {
			return nil, true, formulaError("#NUM!", name+" needs a yield that is not negative")
		}
		periodYield := yield / float64(frequency)
		share := remaining / periodDays
		var value, weighted float64
		for index := 1; index <= count; index++ {
			time := (float64(index) - 1 + share) / float64(frequency)
			cash := coupon
			if index == count {
				cash += redemption
			}
			present := cash / math.Pow(1+periodYield, float64(index)-1+share)
			value += present
			weighted += present * time
		}
		if value == 0 {
			return nil, true, formulaError("#NUM!", name+" cannot value this bond")
		}
		macaulay := weighted / value
		if name == "DURATION" {
			return macaulay, true, nil
		}
		return macaulay / (1 + periodYield), true, nil
	}
	return nil, false, nil
}

// evaluateAccruedAtMaturity 는 만기에 한 번만 이자를 주는 증권의 경과이자다.
func evaluateAccruedAtMaturity(values []any) (any, bool, error) {
	if len(values) < 4 || len(values) > 5 {
		return nil, true, argError("ACCRINTM")
	}
	issue, settlement, basis, err := securityDates("ACCRINTM", values, 4)
	if err != nil {
		return nil, true, err
	}
	rate, par, err := twoNumbers("ACCRINTM", values[2:4])
	if err != nil {
		return nil, true, err
	}
	if rate <= 0 || par <= 0 {
		return nil, true, formulaError("#NUM!", "ACCRINTM needs a positive rate and par value")
	}
	return par * rate * yearFraction(issue, settlement, basis), true, nil
}
