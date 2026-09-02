package formula

import (
	"math"
)

// 분포 함수들. 값이 조금만 어긋나도 사람은 알아채지 못하므로, 시험의
// 기댓값은 기억이 아니라 따로 계산한 것을 쓴다.
func evaluateDistribution(name string, values []any) (any, bool, error) {
	switch name {
	case "NORMSDIST", "NORMSINV", "GAUSS", "FISHER", "FISHERINV":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		switch name {
		case "NORMSDIST":
			return standardNormalCDF(number), true, nil
		case "GAUSS":
			return standardNormalCDF(number) - 0.5, true, nil
		case "NORMSINV":
			if number <= 0 || number >= 1 {
				return nil, true, formulaError("#NUM!", "NORMSINV needs a probability between 0 and 1")
			}
			return standardNormalInverse(number), true, nil
		case "FISHER":
			if number <= -1 || number >= 1 {
				return nil, true, formulaError("#NUM!", "FISHER needs a number between -1 and 1")
			}
			return math.Atanh(number), true, nil
		case "FISHERINV":
			return math.Tanh(number), true, nil
		}
	case "NORMDIST", "LOGNORMDIST", "NORMINV", "LOGINV", "STANDARDIZE", "CONFIDENCE":
		return evaluateNormalFamily(name, values)
	case "EXPONDIST", "POISSON", "WEIBULL", "BINOMDIST", "NEGBINOMDIST", "HYPGEOMDIST", "CRITBINOM":
		return evaluateDiscreteFamily(name, values)
	case "AVEDEV", "DEVSQ", "SKEW", "KURT", "MODE.MULT":
		return evaluateSpread(name, values)
	}
	return nil, false, nil
}

// standardNormalCDF 는 표준정규분포의 누적값이다. erf 로 바로 쓴다.
func standardNormalCDF(z float64) float64 {
	return 0.5 * math.Erfc(-z/math.Sqrt2)
}

func standardNormalPDF(z float64) float64 {
	return math.Exp(-z*z/2) / math.Sqrt(2*math.Pi)
}

// standardNormalInverse 는 누적값에서 z 를 되돌린다. Acklam 의 유리식으로
// 시작해 한 번 다듬는다 — 유리식만으로는 소수점 아홉째 자리에서 어긋나고,
// 그 값을 다시 정규분포에 넣는 셈에서 눈에 띈다.
func standardNormalInverse(probability float64) float64 {
	const (
		a1, a2, a3, a4, a5, a6 = -3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00
		b1, b2, b3, b4, b5     = -5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01
		c1, c2, c3, c4, c5, c6 = -7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00
		d1, d2, d3, d4         = 7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00
		lower, upper           = 0.02425, 1 - 0.02425
	)
	var z float64
	switch {
	case probability < lower:
		q := math.Sqrt(-2 * math.Log(probability))
		z = (((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) / ((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	case probability <= upper:
		q := probability - 0.5
		r := q * q
		z = (((((a1*r+a2)*r+a3)*r+a4)*r+a5)*r + a6) * q / (((((b1*r+b2)*r+b3)*r+b4)*r+b5)*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-probability))
		z = -(((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) / ((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	}
	// 한 번의 핼리 보정. 여기서 거의 기계 정밀도까지 간다.
	density := standardNormalPDF(z)
	if density > 0 {
		error := standardNormalCDF(z) - probability
		step := error / density
		z -= step / (1 + z*step/2)
	}
	return z
}

func evaluateNormalFamily(name string, values []any) (any, bool, error) {
	switch name {
	case "STANDARDIZE":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		number, mean, deviation, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if deviation <= 0 {
			return nil, true, formulaError("#NUM!", "STANDARDIZE needs a positive standard deviation")
		}
		return (number - mean) / deviation, true, nil
	case "CONFIDENCE":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		alpha, deviation, size, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if alpha <= 0 || alpha >= 1 || deviation <= 0 || size < 1 {
			return nil, true, formulaError("#NUM!", "CONFIDENCE needs 0 < alpha < 1, a positive deviation and a size of 1 or more")
		}
		return standardNormalInverse(1-alpha/2) * deviation / math.Sqrt(math.Trunc(size)), true, nil
	case "NORMINV", "LOGINV":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		probability, mean, deviation, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if probability <= 0 || probability >= 1 || deviation <= 0 {
			return nil, true, formulaError("#NUM!", name+" needs a probability between 0 and 1 and a positive deviation")
		}
		z := mean + deviation*standardNormalInverse(probability)
		if name == "LOGINV" {
			return math.Exp(z), true, nil
		}
		return z, true, nil
	case "NORMDIST", "LOGNORMDIST":
		// LOGNORMDIST 는 누적값만 있다. NORMDIST 는 네 번째 인수로 고른다.
		if name == "LOGNORMDIST" && len(values) != 3 {
			return nil, true, argError(name)
		}
		if name == "NORMDIST" && (len(values) < 3 || len(values) > 4) {
			return nil, true, argError(name)
		}
		number, mean, deviation, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if deviation <= 0 {
			return nil, true, formulaError("#NUM!", name+" needs a positive standard deviation")
		}
		if name == "LOGNORMDIST" {
			if number <= 0 {
				return nil, true, formulaError("#NUM!", "LOGNORMDIST needs a positive number")
			}
			return standardNormalCDF((math.Log(number) - mean) / deviation), true, nil
		}
		cumulative := true
		if len(values) == 4 && !omitted(values[3]) {
			cumulative = truthy(values[3])
		}
		z := (number - mean) / deviation
		if cumulative {
			return standardNormalCDF(z), true, nil
		}
		return standardNormalPDF(z) / deviation, true, nil
	}
	return nil, false, nil
}

func evaluateDiscreteFamily(name string, values []any) (any, bool, error) {
	switch name {
	case "EXPONDIST":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		number, rate, err := twoNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if number < 0 || rate <= 0 {
			return nil, true, formulaError("#NUM!", "EXPONDIST needs a number of 0 or more and a positive rate")
		}
		if truthy(values[2]) {
			return 1 - math.Exp(-rate*number), true, nil
		}
		return rate * math.Exp(-rate*number), true, nil
	case "POISSON":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		count, mean, err := twoNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		count = math.Trunc(count)
		if count < 0 || mean < 0 {
			return nil, true, formulaError("#NUM!", "POISSON needs numbers that are not negative")
		}
		if truthy(values[2]) {
			total := 0.0
			for k := 0.0; k <= count; k++ {
				total += poissonMass(k, mean)
			}
			return total, true, nil
		}
		return poissonMass(count, mean), true, nil
	case "WEIBULL":
		if len(values) != 4 {
			return nil, true, argError(name)
		}
		number, shape, scale, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if number < 0 || shape <= 0 || scale <= 0 {
			return nil, true, formulaError("#NUM!", "WEIBULL needs a number of 0 or more and positive parameters")
		}
		ratio := math.Pow(number/scale, shape)
		if truthy(values[3]) {
			return 1 - math.Exp(-ratio), true, nil
		}
		return shape / math.Pow(scale, shape) * math.Pow(number, shape-1) * math.Exp(-ratio), true, nil
	case "BINOMDIST":
		if len(values) != 4 {
			return nil, true, argError(name)
		}
		successes, trials, chance, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		successes, trials = math.Trunc(successes), math.Trunc(trials)
		if successes < 0 || successes > trials || chance < 0 || chance > 1 {
			return nil, true, formulaError("#NUM!", "BINOMDIST needs 0 <= successes <= trials and a probability between 0 and 1")
		}
		if truthy(values[3]) {
			total := 0.0
			for k := 0.0; k <= successes; k++ {
				total += binomialMass(k, trials, chance)
			}
			return total, true, nil
		}
		return binomialMass(successes, trials, chance), true, nil
	case "NEGBINOMDIST":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		failures, successes, chance, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		failures, successes = math.Trunc(failures), math.Trunc(successes)
		if failures < 0 || successes < 1 || chance <= 0 || chance > 1 {
			return nil, true, formulaError("#NUM!", "NEGBINOMDIST needs whole counts and a probability between 0 and 1")
		}
		return binomial(failures+successes-1, failures) * math.Pow(chance, successes) * math.Pow(1-chance, failures), true, nil
	case "HYPGEOMDIST":
		if len(values) != 4 {
			return nil, true, argError(name)
		}
		drawn, sample, marked, population, err := fourNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		drawn, sample, marked, population = math.Trunc(drawn), math.Trunc(sample), math.Trunc(marked), math.Trunc(population)
		if drawn < 0 || drawn > sample || drawn > marked || sample > population || marked > population ||
			sample-drawn > population-marked {
			return nil, true, formulaError("#NUM!", "HYPGEOMDIST counts do not fit the population")
		}
		return binomial(marked, drawn) * binomial(population-marked, sample-drawn) / binomial(population, sample), true, nil
	case "CRITBINOM":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		trials, chance, alpha, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		trials = math.Trunc(trials)
		if trials < 0 || chance < 0 || chance > 1 || alpha <= 0 || alpha >= 1 {
			return nil, true, formulaError("#NUM!", "CRITBINOM needs whole trials, a probability between 0 and 1 and 0 < alpha < 1")
		}
		total := 0.0
		for k := 0.0; k <= trials; k++ {
			total += binomialMass(k, trials, chance)
			if total >= alpha {
				return k, true, nil
			}
		}
		return trials, true, nil
	}
	return nil, false, nil
}

func evaluateSpread(name string, values []any) (any, bool, error) {
	numbers := numericValues(values)
	switch name {
	case "AVEDEV":
		if len(numbers) == 0 {
			return nil, true, formulaError("#NUM!", "AVEDEV needs numbers")
		}
		average := mean(numbers)
		total := 0.0
		for _, number := range numbers {
			total += math.Abs(number - average)
		}
		return total / float64(len(numbers)), true, nil
	case "DEVSQ":
		if len(numbers) == 0 {
			return nil, true, formulaError("#NUM!", "DEVSQ needs numbers")
		}
		average := mean(numbers)
		total := 0.0
		for _, number := range numbers {
			total += (number - average) * (number - average)
		}
		return total, true, nil
	case "SKEW", "KURT":
		count := float64(len(numbers))
		needed := 3.0
		if name == "KURT" {
			needed = 4
		}
		if count < needed {
			return nil, true, formulaError("#DIV/0!", name+" needs more numbers")
		}
		average := mean(numbers)
		deviation := math.Sqrt(populationVariance(numbers, true))
		if deviation == 0 {
			return nil, true, formulaError("#DIV/0!", name+" needs numbers that are not all the same")
		}
		total := 0.0
		for _, number := range numbers {
			scaled := (number - average) / deviation
			if name == "SKEW" {
				total += scaled * scaled * scaled
			} else {
				total += scaled * scaled * scaled * scaled
			}
		}
		if name == "SKEW" {
			return count / ((count - 1) * (count - 2)) * total, true, nil
		}
		return count*(count+1)/((count-1)*(count-2)*(count-3))*total -
			3*(count-1)*(count-1)/((count-2)*(count-3)), true, nil
	case "MODE.MULT":
		if len(numbers) == 0 {
			return nil, true, formulaError("#N/A", "MODE.MULT needs numbers")
		}
		counts := map[float64]int{}
		for _, number := range numbers {
			counts[number]++
		}
		best := 0
		for _, count := range counts {
			if count > best {
				best = count
			}
		}
		if best < 2 {
			return nil, true, formulaError("#N/A", "MODE.MULT found no repeated number")
		}
		// 같은 횟수로 가장 자주 나온 값이 여럿이면 모두 돌려준다. 처음
		// 나온 차례를 지킨다 — 엑셀이 그렇게 늘어놓는다.
		seen := map[float64]bool{}
		modes := make([]any, 0, 4)
		for _, number := range numbers {
			if counts[number] == best && !seen[number] {
				seen[number] = true
				modes = append(modes, number)
			}
		}
		return arrayValue{rows: len(modes), columns: 1, values: modes}, true, nil
	}
	return nil, false, nil
}

func poissonMass(count, mean float64) float64 {
	logMass := -mean + count*math.Log(mean) - logFactorial(count)
	if mean == 0 {
		if count == 0 {
			return 1
		}
		return 0
	}
	return math.Exp(logMass)
}

func binomialMass(successes, trials, chance float64) float64 {
	switch {
	case chance == 0:
		if successes == 0 {
			return 1
		}
		return 0
	case chance == 1:
		if successes == trials {
			return 1
		}
		return 0
	}
	logMass := logFactorial(trials) - logFactorial(successes) - logFactorial(trials-successes) +
		successes*math.Log(chance) + (trials-successes)*math.Log(1-chance)
	return math.Exp(logMass)
}

// logFactorial 은 큰 수에서도 넘치지 않도록 감마의 로그를 쓴다. 곱해 나가면
// 170! 을 넘는 순간 무한대가 된다.
func logFactorial(number float64) float64 {
	result, _ := math.Lgamma(number + 1)
	return result
}

func twoNumbers(name string, values []any) (float64, float64, error) {
	first, firstOK := toNumber(values[0])
	second, secondOK := toNumber(values[1])
	if !firstOK || !secondOK {
		return 0, 0, formulaError("#VALUE!", name+" requires numbers")
	}
	return first, second, nil
}

func fourNumbers(name string, values []any) (float64, float64, float64, float64, error) {
	first, second, third, err := threeNumbers(name, values)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	fourth, ok := toNumber(values[3])
	if !ok {
		return 0, 0, 0, 0, formulaError("#VALUE!", name+" requires numbers")
	}
	return first, second, third, fourth, nil
}

// 아래 다섯은 범위의 모양을 그대로 봐야 한다. 낱낱이 펴면 어느 값이 자료이고
// 어느 값이 찾는 값인지 알 수 없다.
func evaluateRangeStatistics(name string, values []any) (any, bool, error) {
	switch name {
	case "PERCENTRANK", "TRIMMEAN", "ZTEST", "STEYX", "PROB":
	default:
		return nil, false, nil
	}
	if len(values) < 2 {
		return nil, true, argError(name)
	}
	first, err := toArray(values[0])
	if err != nil {
		return nil, true, err
	}
	numbers := numericValues(first.values)
	switch name {
	case "PERCENTRANK":
		if len(values) > 3 {
			return nil, true, argError(name)
		}
		target, ok := toNumber(values[1])
		if !ok {
			return nil, true, formulaError("#VALUE!", "PERCENTRANK requires a number")
		}
		digits := 3.0
		if len(values) == 3 && !omitted(values[2]) {
			if digits, ok = toNumber(values[2]); !ok || digits < 1 {
				return nil, true, formulaError("#NUM!", "PERCENTRANK needs at least one digit")
			}
		}
		if len(numbers) < 2 {
			return nil, true, formulaError("#NUM!", "PERCENTRANK needs at least two numbers")
		}
		sorted := sortedNumbers(numbers)
		if target < sorted[0] || target > sorted[len(sorted)-1] {
			return nil, true, formulaError("#N/A", "PERCENTRANK cannot rank a number outside the range")
		}
		last := float64(len(sorted) - 1)
		rank := 0.0
		found := false
		for index, number := range sorted {
			if number == target {
				rank, found = float64(index)/last, true
				break
			}
		}
		if !found {
			for index := 0; index < len(sorted)-1; index++ {
				if sorted[index] < target && target < sorted[index+1] {
					// 사이에 있으면 두 값 사이를 나눠 센다.
					share := (target - sorted[index]) / (sorted[index+1] - sorted[index])
					rank = (float64(index) + share) / last
					break
				}
			}
		}
		// 엑셀은 자리 수를 반올림하지 않고 잘라 낸다. 자르는 일은 TRUNC 와
		// 같은 십진 셈에 맡긴다. 이진 실수로 밀면 순위가 마침 0.29 일 때
		// 두 자리로 잘라 0.28 이 나온다.
		return decimalRound(rank, decimalPlaces(digits), roundTowardZero), true, nil
	case "TRIMMEAN":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		share, ok := toNumber(values[1])
		if !ok {
			return nil, true, formulaError("#VALUE!", "TRIMMEAN requires a number")
		}
		if share < 0 || share >= 1 {
			return nil, true, formulaError("#NUM!", "TRIMMEAN needs a share between 0 and 1")
		}
		if len(numbers) == 0 {
			return nil, true, formulaError("#NUM!", "TRIMMEAN needs numbers")
		}
		sorted := sortedNumbers(numbers)
		// 위아래에서 같은 개수를 덜어 내야 하므로 짝수로 내림한다.
		dropped := int(math.Floor(float64(len(sorted)) * share))
		dropped -= dropped % 2
		dropped /= 2
		kept := sorted[dropped : len(sorted)-dropped]
		if len(kept) == 0 {
			return nil, true, formulaError("#NUM!", "TRIMMEAN trimmed away every number")
		}
		return mean(kept), true, nil
	case "ZTEST":
		if len(values) > 3 {
			return nil, true, argError(name)
		}
		target, ok := toNumber(values[1])
		if !ok {
			return nil, true, formulaError("#VALUE!", "ZTEST requires a number")
		}
		if len(numbers) < 2 {
			return nil, true, formulaError("#DIV/0!", "ZTEST needs at least two numbers")
		}
		deviation := math.Sqrt(populationVariance(numbers, true))
		if len(values) == 3 && !omitted(values[2]) {
			if deviation, ok = toNumber(values[2]); !ok {
				return nil, true, formulaError("#VALUE!", "ZTEST requires a number")
			}
		}
		if deviation <= 0 {
			return nil, true, formulaError("#NUM!", "ZTEST needs a positive standard deviation")
		}
		z := (mean(numbers) - target) / (deviation / math.Sqrt(float64(len(numbers))))
		return 1 - standardNormalCDF(z), true, nil
	case "STEYX":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		second, arrayErr := toArray(values[1])
		if arrayErr != nil {
			return nil, true, arrayErr
		}
		known := numericValues(second.values)
		if len(numbers) != len(known) {
			return nil, true, formulaError("#N/A", "STEYX needs two ranges of the same size")
		}
		if len(numbers) < 3 {
			return nil, true, formulaError("#DIV/0!", "STEYX needs at least three pairs")
		}
		meanY, meanX := mean(numbers), mean(known)
		var sxx, syy, sxy float64
		for index := range numbers {
			dx, dy := known[index]-meanX, numbers[index]-meanY
			sxx += dx * dx
			syy += dy * dy
			sxy += dx * dy
		}
		if sxx == 0 {
			return nil, true, formulaError("#DIV/0!", "STEYX needs x values that are not all the same")
		}
		return math.Sqrt((syy - sxy*sxy/sxx) / float64(len(numbers)-2)), true, nil
	case "PROB":
		if len(values) < 3 || len(values) > 4 {
			return nil, true, argError(name)
		}
		second, arrayErr := toArray(values[1])
		if arrayErr != nil {
			return nil, true, arrayErr
		}
		chances := numericValues(second.values)
		if len(numbers) != len(chances) {
			return nil, true, formulaError("#N/A", "PROB needs two ranges of the same size")
		}
		total := 0.0
		for _, chance := range chances {
			if chance <= 0 || chance > 1 {
				return nil, true, formulaError("#NUM!", "PROB needs probabilities above 0 and up to 1")
			}
			total += chance
		}
		// 합이 1 이 아니면 확률표가 아니다. 조용히 세면 뜻 없는 답이 나온다.
		if math.Abs(total-1) > 1e-9 {
			return nil, true, formulaError("#NUM!", "PROB needs probabilities that add up to 1")
		}
		lower, ok := toNumber(values[2])
		if !ok {
			return nil, true, formulaError("#VALUE!", "PROB requires a number")
		}
		upper := lower
		if len(values) == 4 && !omitted(values[3]) {
			if upper, ok = toNumber(values[3]); !ok {
				return nil, true, formulaError("#VALUE!", "PROB requires a number")
			}
		}
		if upper < lower {
			lower, upper = upper, lower
		}
		matched := 0.0
		for index, number := range numbers {
			if number >= lower && number <= upper {
				matched += chances[index]
			}
		}
		return matched, true, nil
	}
	return nil, false, nil
}
