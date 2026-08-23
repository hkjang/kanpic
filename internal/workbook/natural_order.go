package workbook

import "strings"

// compareNatural orders text the way a person reads it: the digits inside a
// value count as one number rather than as characters. Sorting month names as
// plain text puts 10월 and 12월 before 1월, and 항목10 before 항목2, because '0'
// sorts before '월' and before '2'. Nobody means that.
//
// This is not what Excel or Google Sheets do — both sort character by
// character and produce that order. It is a deliberate difference, and the
// sort dialog lets a reader turn it off.
func compareNatural(left, right string) int {
	leftIndex, rightIndex := 0, 0
	for leftIndex < len(left) && rightIndex < len(right) {
		leftDigit, rightDigit := isASCIIDigit(left[leftIndex]), isASCIIDigit(right[rightIndex])
		if leftDigit && rightDigit {
			leftEnd, rightEnd := digitRunEnd(left, leftIndex), digitRunEnd(right, rightIndex)
			if comparison := compareDigitRuns(left[leftIndex:leftEnd], right[rightIndex:rightEnd]); comparison != 0 {
				return comparison
			}
			leftIndex, rightIndex = leftEnd, rightEnd
			continue
		}
		if leftDigit != rightDigit {
			// 숫자가 글자보다 앞에 온다.
			if leftDigit {
				return -1
			}
			return 1
		}
		if left[leftIndex] != right[rightIndex] {
			if left[leftIndex] < right[rightIndex] {
				return -1
			}
			return 1
		}
		leftIndex++
		rightIndex++
	}
	switch {
	case leftIndex < len(left):
		return 1
	case rightIndex < len(right):
		return -1
	default:
		return 0
	}
}

// compareDigitRuns compares two runs of digits as numbers without turning them
// into floats, so a forty digit account number still orders exactly. Once the
// leading zeros are gone the longer run is the larger number, and runs of the
// same length compare character by character.
func compareDigitRuns(left, right string) int {
	leftDigits, rightDigits := strings.TrimLeft(left, "0"), strings.TrimLeft(right, "0")
	if len(leftDigits) != len(rightDigits) {
		if len(leftDigits) < len(rightDigits) {
			return -1
		}
		return 1
	}
	if comparison := strings.Compare(leftDigits, rightDigits); comparison != 0 {
		return comparison
	}
	// 같은 수다. 자릿수를 맞춰 쓴 쪽(007)을 앞에 두어 순서가 흔들리지 않게 한다.
	if len(left) != len(right) {
		if len(left) > len(right) {
			return -1
		}
		return 1
	}
	return 0
}

func isASCIIDigit(character byte) bool { return character >= '0' && character <= '9' }

func digitRunEnd(value string, index int) int {
	end := index
	for end < len(value) && isASCIIDigit(value[end]) {
		end++
	}
	return end
}
