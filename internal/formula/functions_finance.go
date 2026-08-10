package formula

import "math"

// evaluateFinance implements the time-value-of-money family. The sign
// convention matches Google Sheets and Excel: money paid out is negative, so
// PMT on a loan returns a negative payment.
func evaluateFinance(name string, values []any) (any, bool, error) {
	switch name {
	case "PMT", "IPMT", "PPMT", "FV", "PV", "NPER":
		return evaluateAnnuity(name, values)
	case "RATE":
		if len(values) < 3 || len(values) > 6 {
			return nil, true, argError(name)
		}
		periods, payment, present, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		future, dueAtStart, err := annuityTail(name, values, 3)
		if err != nil {
			return nil, true, err
		}
		guess := 0.1
		if len(values) == 6 {
			supplied, ok := toNumber(values[5])
			if !ok {
				return nil, true, formulaError("#VALUE!", "RATE guess must be a number")
			}
			guess = supplied
		}
		rate, ok := solveRate(periods, payment, present, future, dueAtStart, guess)
		if !ok {
			return nil, true, formulaError("#NUM!", "RATE did not converge")
		}
		return rate, true, nil
	case "NPV":
		if len(values) < 2 {
			return nil, true, argError(name)
		}
		rate, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "NPV requires a rate")
		}
		if rate <= -1 {
			return nil, true, formulaError("#NUM!", "NPV rate must be above -100%")
		}
		total, period := 0.0, 1.0
		for _, value := range values[1:] {
			amount, isNumber := toNumber(value)
			if !isNumber || value == nil {
				continue
			}
			total += amount / math.Pow(1+rate, period)
			period++
		}
		return total, true, nil
	case "SLN":
		cost, salvage, life, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if life == 0 {
			return nil, true, formulaError("#DIV/0!", "SLN life must not be zero")
		}
		return (cost - salvage) / life, true, nil
	case "SYD":
		if len(values) != 4 {
			return nil, true, argError(name)
		}
		cost, salvage, life, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		period, ok := toNumber(values[3])
		if !ok || period < 1 || period > life {
			return nil, true, formulaError("#NUM!", "SYD period must fall inside the asset life")
		}
		return (cost - salvage) * (life - period + 1) * 2 / (life * (life + 1)), true, nil
	case "DDB", "DB":
		if len(values) < 4 || len(values) > 5 {
			return nil, true, argError(name)
		}
		cost, salvage, life, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		period, ok := toNumber(values[3])
		if !ok || period < 1 || life <= 0 {
			return nil, true, formulaError("#NUM!", name+" needs a positive life and period")
		}
		factor := 2.0
		if len(values) == 5 {
			if factor, ok = toNumber(values[4]); !ok || factor <= 0 {
				return nil, true, formulaError("#NUM!", "DDB factor must be positive")
			}
		}
		return decliningBalance(cost, salvage, life, period, factor), true, nil
	case "EFFECT", "NOMINAL":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		rate, rateOK := toNumber(values[0])
		periods, periodsOK := toNumber(values[1])
		if !rateOK || !periodsOK || periods < 1 || rate <= -1 {
			return nil, true, formulaError("#NUM!", name+" needs a rate and at least one period")
		}
		periods = math.Trunc(periods)
		if name == "EFFECT" {
			return math.Pow(1+rate/periods, periods) - 1, true, nil
		}
		return (math.Pow(1+rate, 1/periods) - 1) * periods, true, nil
	case "RRI":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		periods, present, future, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if periods <= 0 || present == 0 {
			return nil, true, formulaError("#NUM!", "RRI needs periods and a present value")
		}
		return math.Pow(future/present, 1/periods) - 1, true, nil
	case "PDURATION":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		rate, present, future, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if rate <= 0 || present <= 0 || future <= 0 {
			return nil, true, formulaError("#NUM!", "PDURATION needs positive values")
		}
		return (math.Log(future) - math.Log(present)) / math.Log(1+rate), true, nil
	case "CUMIPMT", "CUMPRINC":
		if len(values) < 6 || len(values) > 7 {
			return nil, true, argError(name)
		}
		rate, periods, present, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		first, firstOK := toNumber(values[3])
		last, lastOK := toNumber(values[4])
		if !firstOK || !lastOK || first < 1 || last < first || last > periods {
			return nil, true, formulaError("#NUM!", name+" period range is outside the schedule")
		}
		dueAtStart := false
		if typeValue, ok := toNumber(values[5]); ok && typeValue != 0 {
			dueAtStart = true
		}
		total := 0.0
		for period := int(first); period <= int(last); period++ {
			interest, principal := periodSplit(rate, periods, present, 0, dueAtStart, float64(period))
			if name == "CUMIPMT" {
				total += interest
			} else {
				total += principal
			}
		}
		return total, true, nil
	}
	return nil, false, nil
}

// evaluateCashflow covers the functions whose first argument is a whole series
// of cash flows, which has to stay separate from the arguments after it.
func evaluateCashflow(name string, arguments []any) (any, bool, error) {
	switch name {
	case "IRR":
		if len(arguments) < 1 || len(arguments) > 2 {
			return nil, true, argError(name)
		}
		guess := 0.1
		if len(arguments) == 2 {
			supplied, ok := toNumber(scalarOrFirst(arguments[1]))
			if !ok {
				return nil, true, formulaError("#VALUE!", "IRR guess must be a number")
			}
			guess = supplied
		}
		rate, ok := solveInternalRate(numericValues(flatten(arguments[0])), guess)
		if !ok {
			return nil, true, formulaError("#NUM!", "IRR did not converge")
		}
		return rate, true, nil
	case "MIRR":
		if len(arguments) != 3 {
			return nil, true, argError(name)
		}
		finance, financeOK := toNumber(scalarOrFirst(arguments[1]))
		reinvest, reinvestOK := toNumber(scalarOrFirst(arguments[2]))
		if !financeOK || !reinvestOK {
			return nil, true, formulaError("#VALUE!", "MIRR requires two rates")
		}
		return modifiedInternalRate(numericValues(flatten(arguments[0])), finance, reinvest)
	case "XNPV", "XIRR":
		if (name == "XNPV" && len(arguments) != 3) || (name == "XIRR" && (len(arguments) < 2 || len(arguments) > 3)) {
			return nil, true, argError(name)
		}
		amountIndex, dateIndex := 1, 2
		if name == "XIRR" {
			amountIndex, dateIndex = 0, 1
		}
		amounts := flatten(arguments[amountIndex])
		dates := flatten(arguments[dateIndex])
		if len(amounts) != len(dates) {
			return nil, true, formulaError("#NUM!", name+" needs one date for every amount")
		}
		flows := make([]float64, 0, len(amounts))
		offsets := make([]float64, 0, len(amounts))
		var start float64
		for index := range amounts {
			amount, amountOK := toNumber(amounts[index])
			moment, dateOK := parseDate(dates[index])
			if !amountOK || !dateOK {
				return nil, true, formulaError("#VALUE!", name+" needs numbers paired with dates")
			}
			day := float64(moment.Unix()) / 86400
			if index == 0 {
				start = day
			}
			flows = append(flows, amount)
			offsets = append(offsets, day-start)
		}
		if len(flows) < 2 {
			return nil, true, formulaError("#NUM!", name+" needs at least two cash flows")
		}
		present := func(rate float64) float64 {
			if rate <= -1 {
				return math.NaN()
			}
			total := 0.0
			for index, amount := range flows {
				total += amount / math.Pow(1+rate, offsets[index]/365)
			}
			return total
		}
		if name == "XNPV" {
			rate, ok := toNumber(scalarOrFirst(arguments[0]))
			if !ok {
				return nil, true, formulaError("#VALUE!", "XNPV requires a rate")
			}
			return present(rate), true, nil
		}
		guess := 0.1
		if len(arguments) == 3 {
			if supplied, ok := toNumber(scalarOrFirst(arguments[2])); ok {
				guess = supplied
			}
		}
		rate, ok := solveNewton(present, guess)
		if !ok {
			return nil, true, formulaError("#NUM!", "XIRR did not converge")
		}
		return rate, true, nil
	}
	return nil, false, nil
}

// evaluateAnnuity solves the standard annuity equation for whichever term the
// caller asked for.
func evaluateAnnuity(name string, values []any) (any, bool, error) {
	switch name {
	case "PMT":
		if len(values) < 3 || len(values) > 5 {
			return nil, true, argError(name)
		}
		rate, periods, present, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		future, dueAtStart, err := annuityTail(name, values, 3)
		if err != nil {
			return nil, true, err
		}
		if periods == 0 {
			return nil, true, formulaError("#NUM!", "PMT needs at least one period")
		}
		return payment(rate, periods, present, future, dueAtStart), true, nil
	case "IPMT", "PPMT":
		if len(values) < 4 || len(values) > 6 {
			return nil, true, argError(name)
		}
		rate, ok1 := toNumber(values[0])
		period, ok2 := toNumber(values[1])
		periods, ok3 := toNumber(values[2])
		present, ok4 := toNumber(values[3])
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return nil, true, formulaError("#VALUE!", name+" requires numbers")
		}
		if period < 1 || period > periods {
			return nil, true, formulaError("#NUM!", name+" period is outside the schedule")
		}
		future, dueAtStart, err := annuityTail(name, values, 4)
		if err != nil {
			return nil, true, err
		}
		interest, principal := periodSplit(rate, periods, present, future, dueAtStart, period)
		if name == "IPMT" {
			return interest, true, nil
		}
		return principal, true, nil
	case "FV", "PV":
		if len(values) < 3 || len(values) > 5 {
			return nil, true, argError(name)
		}
		rate, periods, third, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		tail, dueAtStart, err := annuityTail(name, values, 3)
		if err != nil {
			return nil, true, err
		}
		due := 0.0
		if dueAtStart {
			due = 1
		}
		if name == "FV" {
			// third is the payment, tail the present value.
			if rate == 0 {
				return -(tail + third*periods), true, nil
			}
			growth := math.Pow(1+rate, periods)
			return -(tail*growth + third*(1+rate*due)*(growth-1)/rate), true, nil
		}
		if rate == 0 {
			return -(tail + third*periods), true, nil
		}
		growth := math.Pow(1+rate, periods)
		return -(tail + third*(1+rate*due)*(growth-1)/rate) / growth, true, nil
	case "NPER":
		if len(values) < 3 || len(values) > 5 {
			return nil, true, argError(name)
		}
		rate, pay, present, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		future, dueAtStart, err := annuityTail(name, values, 3)
		if err != nil {
			return nil, true, err
		}
		if rate == 0 {
			if pay == 0 {
				return nil, true, formulaError("#DIV/0!", "NPER needs a payment when the rate is zero")
			}
			return -(present + future) / pay, true, nil
		}
		due := 0.0
		if dueAtStart {
			due = 1
		}
		adjusted := pay * (1 + rate*due) / rate
		if adjusted-future == 0 || (present+adjusted) == 0 {
			return nil, true, formulaError("#NUM!", "NPER has no solution for these values")
		}
		ratio := (adjusted - future) / (present + adjusted)
		if ratio <= 0 {
			return nil, true, formulaError("#NUM!", "NPER has no solution for these values")
		}
		return math.Log(ratio) / math.Log(1+rate), true, nil
	}
	return nil, false, nil
}

func payment(rate, periods, present, future float64, dueAtStart bool) float64 {
	if rate == 0 {
		return -(present + future) / periods
	}
	due := 0.0
	if dueAtStart {
		due = 1
	}
	growth := math.Pow(1+rate, periods)
	return -(present*growth + future) * rate / ((1 + rate*due) * (growth - 1))
}

// periodSplit reports how much of one instalment is interest and how much is
// principal, which is what IPMT, PPMT and the cumulative pair need.
func periodSplit(rate, periods, present, future float64, dueAtStart bool, period float64) (float64, float64) {
	instalment := payment(rate, periods, present, future, dueAtStart)
	balance := present
	var interest, principal float64
	for current := 1.0; current <= period; current++ {
		// Interest accrues on what is still owed; the rest of the instalment
		// reduces the balance. Both are reported as money paid out.
		charge := balance * rate
		if dueAtStart && current == 1 {
			charge = 0
		}
		interest = -charge
		principal = instalment + charge
		balance += principal
	}
	return interest, principal
}

func decliningBalance(cost, salvage, life, period, factor float64) float64 {
	rate := factor / life
	if rate > 1 {
		rate = 1
	}
	remaining := cost
	depreciation := 0.0
	for current := 1.0; current <= period; current++ {
		depreciation = remaining * rate
		if remaining-depreciation < salvage {
			depreciation = math.Max(0, remaining-salvage)
		}
		remaining -= depreciation
	}
	return depreciation
}

func annuityTail(name string, values []any, index int) (float64, bool, error) {
	future := 0.0
	if len(values) > index {
		number, ok := toNumber(values[index])
		if !ok {
			return 0, false, formulaError("#VALUE!", name+" requires numbers")
		}
		future = number
	}
	dueAtStart := false
	if len(values) > index+1 {
		number, ok := toNumber(values[index+1])
		if !ok {
			return 0, false, formulaError("#VALUE!", name+" requires numbers")
		}
		dueAtStart = number != 0
	}
	return future, dueAtStart, nil
}

func threeNumbers(name string, values []any) (float64, float64, float64, error) {
	if len(values) < 3 {
		return 0, 0, 0, argError(name)
	}
	first, ok1 := toNumber(values[0])
	second, ok2 := toNumber(values[1])
	third, ok3 := toNumber(values[2])
	if !ok1 || !ok2 || !ok3 {
		return 0, 0, 0, formulaError("#VALUE!", name+" requires numbers")
	}
	return first, second, third, nil
}

func solveRate(periods, pay, present, future float64, dueAtStart bool, guess float64) (float64, bool) {
	due := 0.0
	if dueAtStart {
		due = 1
	}
	balance := func(rate float64) float64 {
		if rate == 0 {
			return present + pay*periods + future
		}
		growth := math.Pow(1+rate, periods)
		return present*growth + pay*(1+rate*due)*(growth-1)/rate + future
	}
	return solveNewton(balance, guess)
}

func solveInternalRate(amounts []float64, guess float64) (float64, bool) {
	if len(amounts) < 2 {
		return 0, false
	}
	value := func(rate float64) float64 {
		if rate <= -1 {
			return math.NaN()
		}
		total := 0.0
		for period, amount := range amounts {
			total += amount / math.Pow(1+rate, float64(period))
		}
		return total
	}
	return solveNewton(value, guess)
}

func modifiedInternalRate(flows []float64, finance, reinvest float64) (any, bool, error) {
	periods := float64(len(flows) - 1)
	if periods < 1 {
		return nil, true, formulaError("#NUM!", "MIRR needs at least two cash flows")
	}
	var negative, positive float64
	for period, amount := range flows {
		if amount < 0 {
			negative += amount / math.Pow(1+finance, float64(period))
		} else {
			positive += amount * math.Pow(1+reinvest, periods-float64(period))
		}
	}
	if negative == 0 || positive == 0 {
		return nil, true, formulaError("#DIV/0!", "MIRR needs both positive and negative cash flows")
	}
	return math.Pow(positive/-negative, 1/periods) - 1, true, nil
}

// solveNewton finds the root of a well-behaved money function, falling back to
// bisection when the derivative sends it somewhere useless.
func solveNewton(value func(float64) float64, guess float64) (float64, bool) {
	rate := guess
	for iteration := 0; iteration < 128; iteration++ {
		current := value(rate)
		if math.IsNaN(current) {
			break
		}
		if math.Abs(current) < 1e-10 {
			return rate, true
		}
		step := 1e-6 * math.Max(1, math.Abs(rate))
		derivative := (value(rate+step) - value(rate-step)) / (2 * step)
		if derivative == 0 || math.IsNaN(derivative) {
			break
		}
		next := rate - current/derivative
		if math.IsNaN(next) || math.IsInf(next, 0) {
			break
		}
		if math.Abs(next-rate) < 1e-12 {
			return next, true
		}
		rate = next
	}
	low, high := -0.9999999, 1e6
	lowValue, highValue := value(low), value(high)
	if math.IsNaN(lowValue) || math.IsNaN(highValue) || lowValue*highValue > 0 {
		return 0, false
	}
	for iteration := 0; iteration < 256; iteration++ {
		middle := (low + high) / 2
		middleValue := value(middle)
		if math.Abs(middleValue) < 1e-10 {
			return middle, true
		}
		if lowValue*middleValue < 0 {
			high = middle
		} else {
			low, lowValue = middle, middleValue
		}
	}
	return (low + high) / 2, true
}
