package formula

import "math"

// 카이제곱·t·F·베타는 모두 **정규화된 불완전 감마와 불완전 베타** 두 개로
// 풀린다. 그래서 그 둘만 제대로 만들면 열한 개가 한 번에 열린다.
//
// 시험의 기댓값은 여기 쓴 급수·연분수와 전혀 다른 방법 — 확률밀도를
// 적응적 심프슨으로 적분한 값 — 으로 맞췄다. 같은 방법으로 검증하면
// 정의를 잘못 이해한 것을 잡을 수 없다.

const (
	incompleteEpsilon = 3e-16
	incompleteTiny    = 1e-300
	incompleteRounds  = 300
)

// lowerGamma 는 정규화된 하부 불완전 감마 P(a,x) 다. x 가 작으면 급수가,
// 크면 연분수가 빨리 모인다. 경계를 잘못 잡으면 한쪽이 아주 느려진다.
func lowerGamma(a, x float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x < a+1:
		return gammaSeries(a, x)
	default:
		return 1 - gammaContinuedFraction(a, x)
	}
}

func upperGamma(a, x float64) float64 { return 1 - lowerGamma(a, x) }

func gammaSeries(a, x float64) float64 {
	logPrefix, _ := math.Lgamma(a)
	term := 1 / a
	sum := term
	for index := 1; index <= incompleteRounds; index++ {
		term *= x / (a + float64(index))
		sum += term
		if math.Abs(term) < math.Abs(sum)*incompleteEpsilon {
			break
		}
	}
	return sum * math.Exp(-x+a*math.Log(x)-logPrefix)
}

func gammaContinuedFraction(a, x float64) float64 {
	logPrefix, _ := math.Lgamma(a)
	b := x + 1 - a
	c := 1 / incompleteTiny
	d := 1 / b
	result := d
	for index := 1; index <= incompleteRounds; index++ {
		step := -float64(index) * (float64(index) - a)
		b += 2
		d = step*d + b
		if math.Abs(d) < incompleteTiny {
			d = incompleteTiny
		}
		c = b + step/c
		if math.Abs(c) < incompleteTiny {
			c = incompleteTiny
		}
		d = 1 / d
		change := d * c
		result *= change
		if math.Abs(change-1) < incompleteEpsilon {
			break
		}
	}
	return result * math.Exp(-x+a*math.Log(x)-logPrefix)
}

// incompleteBeta 는 정규화된 불완전 베타 I_x(a,b) 다. x 가 가운데를 넘으면
// 대칭을 써서 반대쪽을 센다 — 그래야 연분수가 모인다.
func incompleteBeta(a, b, x float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}
	logGammaAB, _ := math.Lgamma(a + b)
	logGammaA, _ := math.Lgamma(a)
	logGammaB, _ := math.Lgamma(b)
	front := math.Exp(logGammaAB - logGammaA - logGammaB + a*math.Log(x) + b*math.Log1p(-x))
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(a, b, x) / a
	}
	return 1 - front*betaContinuedFraction(b, a, 1-x)/b
}

func betaContinuedFraction(a, b, x float64) float64 {
	c := 1.0
	d := 1 - (a+b)*x/(a+1)
	if math.Abs(d) < incompleteTiny {
		d = incompleteTiny
	}
	d = 1 / d
	result := d
	for index := 1; index <= incompleteRounds; index++ {
		round := float64(index)
		// 짝수 번째와 홀수 번째 항의 꼴이 다르다.
		numerator := round * (b - round) * x / ((a + 2*round - 1) * (a + 2*round))
		d = 1 + numerator*d
		if math.Abs(d) < incompleteTiny {
			d = incompleteTiny
		}
		c = 1 + numerator/c
		if math.Abs(c) < incompleteTiny {
			c = incompleteTiny
		}
		d = 1 / d
		result *= d * c

		numerator = -(a + round) * (a + b + round) * x / ((a + 2*round) * (a + 2*round + 1))
		d = 1 + numerator*d
		if math.Abs(d) < incompleteTiny {
			d = incompleteTiny
		}
		c = 1 + numerator/c
		if math.Abs(c) < incompleteTiny {
			c = incompleteTiny
		}
		d = 1 / d
		change := d * c
		result *= change
		if math.Abs(change-1) < incompleteEpsilon {
			break
		}
	}
	return result
}

// solveDecreasing 은 값이 줄어드는 함수를 되돌린다. 뉴턴법은 꼬리에서 튀어
// 나가므로 구간을 넓혀 잡은 뒤 이분법으로 조인다 — 느리지만 반드시 닿는다.
func solveDecreasing(target float64, start float64, evaluate func(float64) float64) (float64, bool) {
	low, high := 0.0, start
	for rounds := 0; evaluate(high) > target && rounds < 200; rounds++ {
		low = high
		high *= 2
	}
	if evaluate(high) > target {
		return 0, false
	}
	for rounds := 0; rounds < 200; rounds++ {
		middle := (low + high) / 2
		if middle == low || middle == high {
			break
		}
		if evaluate(middle) > target {
			low = middle
		} else {
			high = middle
		}
	}
	return (low + high) / 2, true
}

// solveIncreasing 은 0 과 1 사이에서 값이 커지는 함수를 되돌린다.
func solveIncreasing(target float64, evaluate func(float64) float64) float64 {
	low, high := 0.0, 1.0
	for rounds := 0; rounds < 200; rounds++ {
		middle := (low + high) / 2
		if middle == low || middle == high {
			break
		}
		if evaluate(middle) < target {
			low = middle
		} else {
			high = middle
		}
	}
	return (low + high) / 2
}

// evaluateTestDistributions 는 위의 둘로 풀리는 함수들이다. 어느 꼬리를
// 재는지가 함수마다 다르고, 그것을 잘못 잡으면 값이 그럴듯하게 틀린다.
//
//	CHIDIST, FDIST      오른쪽 꼬리
//	TDIST               tails 로 한쪽·양쪽을 고른다
//	TINV                **양쪽** 이다. 한쪽으로 읽으면 조용히 다른 값이 된다
//	BETADIST            왼쪽 누적
func evaluateTestDistributions(name string, values []any) (any, bool, error) {
	switch name {
	case "CHIDIST", "CHIINV":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		first, degrees, err := twoNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		degrees = math.Trunc(degrees)
		if degrees < 1 || degrees > 1e10 {
			return nil, true, formulaError("#NUM!", name+" needs at least one degree of freedom")
		}
		if name == "CHIDIST" {
			if first < 0 {
				return nil, true, formulaError("#NUM!", "CHIDIST needs a number of 0 or more")
			}
			return upperGamma(degrees/2, first/2), true, nil
		}
		if first <= 0 || first > 1 {
			return nil, true, formulaError("#NUM!", "CHIINV needs a probability above 0 and up to 1")
		}
		answer, ok := solveDecreasing(first, degrees+1, func(x float64) float64 {
			return upperGamma(degrees/2, x/2)
		})
		if !ok {
			return nil, true, formulaError("#NUM!", "CHIINV could not find an answer")
		}
		return answer, true, nil
	case "TDIST", "TINV":
		if name == "TDIST" && len(values) != 3 {
			return nil, true, argError(name)
		}
		if name == "TINV" && len(values) != 2 {
			return nil, true, argError(name)
		}
		first, degrees, err := twoNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		degrees = math.Trunc(degrees)
		if degrees < 1 {
			return nil, true, formulaError("#NUM!", name+" needs at least one degree of freedom")
		}
		rightTail := func(x float64) float64 {
			return 0.5 * incompleteBeta(degrees/2, 0.5, degrees/(degrees+x*x))
		}
		if name == "TDIST" {
			if first < 0 {
				return nil, true, formulaError("#NUM!", "TDIST needs a number of 0 or more")
			}
			tails, ok := toNumber(values[2])
			if !ok || (math.Trunc(tails) != 1 && math.Trunc(tails) != 2) {
				return nil, true, formulaError("#NUM!", "TDIST tails must be 1 or 2")
			}
			return math.Trunc(tails) * rightTail(first), true, nil
		}
		if first <= 0 || first > 1 {
			return nil, true, formulaError("#NUM!", "TINV needs a probability above 0 and up to 1")
		}
		// TINV 는 양쪽이다. 한쪽 꼬리를 절반으로 보고 되돌린다.
		answer, ok := solveDecreasing(first/2, 1, rightTail)
		if !ok {
			return nil, true, formulaError("#NUM!", "TINV could not find an answer")
		}
		return answer, true, nil
	case "FDIST", "FINV":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		first, numerator, denominator, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		numerator, denominator = math.Trunc(numerator), math.Trunc(denominator)
		if numerator < 1 || denominator < 1 {
			return nil, true, formulaError("#NUM!", name+" needs at least one degree of freedom on each side")
		}
		rightTail := func(x float64) float64 {
			if x <= 0 {
				return 1
			}
			return incompleteBeta(denominator/2, numerator/2, denominator/(denominator+numerator*x))
		}
		if name == "FDIST" {
			if first < 0 {
				return nil, true, formulaError("#NUM!", "FDIST needs a number of 0 or more")
			}
			return rightTail(first), true, nil
		}
		if first <= 0 || first > 1 {
			return nil, true, formulaError("#NUM!", "FINV needs a probability above 0 and up to 1")
		}
		answer, ok := solveDecreasing(first, 2, rightTail)
		if !ok {
			return nil, true, formulaError("#NUM!", "FINV could not find an answer")
		}
		return answer, true, nil
	case "BETADIST", "BETAINV":
		if len(values) < 3 || len(values) > 5 {
			return nil, true, argError(name)
		}
		first, alpha, beta, err := threeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if alpha <= 0 || beta <= 0 {
			return nil, true, formulaError("#NUM!", name+" needs positive shape parameters")
		}
		low, high := 0.0, 1.0
		if len(values) >= 4 && !omitted(values[3]) {
			if low, err = singleNumber(name, values[3]); err != nil {
				return nil, true, err
			}
		}
		if len(values) == 5 && !omitted(values[4]) {
			if high, err = singleNumber(name, values[4]); err != nil {
				return nil, true, err
			}
		}
		if high <= low {
			return nil, true, formulaError("#NUM!", name+" needs an upper bound above the lower bound")
		}
		if name == "BETADIST" {
			if first < low || first > high {
				return nil, true, formulaError("#NUM!", "BETADIST needs a number inside the bounds")
			}
			return incompleteBeta(alpha, beta, (first-low)/(high-low)), true, nil
		}
		if first < 0 || first > 1 {
			return nil, true, formulaError("#NUM!", "BETAINV needs a probability between 0 and 1")
		}
		scaled := solveIncreasing(first, func(x float64) float64 { return incompleteBeta(alpha, beta, x) })
		return low + scaled*(high-low), true, nil
	}
	return nil, false, nil
}

func singleNumber(name string, value any) (float64, error) {
	number, ok := toNumber(value)
	if !ok {
		return 0, formulaError("#VALUE!", name+" requires numbers")
	}
	return number, nil
}

// evaluateHypothesisTests 는 두 표본을 견주는 검정들이다. 범위의 모양을
// 봐야 하므로 낱낱이 펴기 전 자리에서 다룬다.
func evaluateHypothesisTests(name string, values []any) (any, bool, error) {
	switch name {
	case "CHITEST", "FTEST", "TTEST":
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
	second, err := toArray(values[1])
	if err != nil {
		return nil, true, err
	}
	left, right := numericValues(first.values), numericValues(second.values)
	switch name {
	case "CHITEST":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		if len(left) != len(right) || len(left) == 0 {
			return nil, true, formulaError("#N/A", "CHITEST needs two ranges of the same size")
		}
		total := 0.0
		for index := range left {
			if right[index] <= 0 {
				return nil, true, formulaError("#DIV/0!", "CHITEST needs expected counts above 0")
			}
			difference := left[index] - right[index]
			total += difference * difference / right[index]
		}
		// 한 줄이나 한 칸짜리면 자유도는 개수 빼기 하나다. 표라면 행과
		// 열에서 각각 하나씩 뺀다.
		degrees := float64(len(left) - 1)
		if first.rows > 1 && first.columns > 1 {
			degrees = float64((first.rows - 1) * (first.columns - 1))
		}
		if degrees < 1 {
			return nil, true, formulaError("#N/A", "CHITEST needs more than one category")
		}
		return upperGamma(degrees/2, total/2), true, nil
	case "FTEST":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		if len(left) < 2 || len(right) < 2 {
			return nil, true, formulaError("#DIV/0!", "FTEST needs at least two numbers in each range")
		}
		leftVariance, rightVariance := populationVariance(left, true), populationVariance(right, true)
		if leftVariance == 0 || rightVariance == 0 {
			return nil, true, formulaError("#DIV/0!", "FTEST needs numbers that are not all the same")
		}
		ratio := leftVariance / rightVariance
		leftDegrees, rightDegrees := float64(len(left)-1), float64(len(right)-1)
		tail := incompleteBeta(rightDegrees/2, leftDegrees/2, rightDegrees/(rightDegrees+leftDegrees*ratio))
		// 양쪽이므로 작은 쪽 꼬리를 두 배 한다.
		if ratio <= 1 {
			tail = 1 - tail
		}
		return 2 * tail, true, nil
	case "TTEST":
		if len(values) != 4 {
			return nil, true, argError(name)
		}
		tails, tailsOK := toNumber(values[2])
		kind, kindOK := toNumber(values[3])
		if !tailsOK || !kindOK {
			return nil, true, formulaError("#VALUE!", "TTEST requires numbers")
		}
		tails, kind = math.Trunc(tails), math.Trunc(kind)
		if tails != 1 && tails != 2 {
			return nil, true, formulaError("#NUM!", "TTEST tails must be 1 or 2")
		}
		if kind < 1 || kind > 3 {
			return nil, true, formulaError("#NUM!", "TTEST type must be 1, 2 or 3")
		}
		if len(left) < 2 || len(right) < 2 {
			return nil, true, formulaError("#DIV/0!", "TTEST needs at least two numbers in each range")
		}
		var statistic, degrees float64
		switch kind {
		case 1:
			if len(left) != len(right) {
				return nil, true, formulaError("#N/A", "a paired TTEST needs two ranges of the same size")
			}
			differences := make([]float64, len(left))
			for index := range left {
				differences[index] = left[index] - right[index]
			}
			deviation := math.Sqrt(populationVariance(differences, true))
			if deviation == 0 {
				return nil, true, formulaError("#DIV/0!", "a paired TTEST needs differences that are not all the same")
			}
			degrees = float64(len(differences) - 1)
			statistic = mean(differences) / (deviation / math.Sqrt(float64(len(differences))))
		case 2:
			leftCount, rightCount := float64(len(left)), float64(len(right))
			pooled := ((leftCount-1)*populationVariance(left, true) + (rightCount-1)*populationVariance(right, true)) /
				(leftCount + rightCount - 2)
			if pooled == 0 {
				return nil, true, formulaError("#DIV/0!", "TTEST needs numbers that are not all the same")
			}
			degrees = leftCount + rightCount - 2
			statistic = (mean(left) - mean(right)) / math.Sqrt(pooled*(1/leftCount+1/rightCount))
		default:
			leftCount, rightCount := float64(len(left)), float64(len(right))
			leftShare := populationVariance(left, true) / leftCount
			rightShare := populationVariance(right, true) / rightCount
			if leftShare+rightShare == 0 {
				return nil, true, formulaError("#DIV/0!", "TTEST needs numbers that are not all the same")
			}
			// 웰치의 자유도는 정수가 아니다. 반올림하면 값이 어긋난다.
			degrees = (leftShare + rightShare) * (leftShare + rightShare) /
				(leftShare*leftShare/(leftCount-1) + rightShare*rightShare/(rightCount-1))
			statistic = (mean(left) - mean(right)) / math.Sqrt(leftShare+rightShare)
		}
		statistic = math.Abs(statistic)
		return tails * 0.5 * incompleteBeta(degrees/2, 0.5, degrees/(degrees+statistic*statistic)), true, nil
	}
	return nil, false, nil
}
