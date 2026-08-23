package presentation

import "strings"

// 한국어 조사는 앞말의 받침에 따라 갈린다. 만들어 낸 문장에 "영업2이(가)" 가
// 남으면 사람이 쓴 글이 아니라는 것이 한눈에 보인다.
//
// 숫자는 읽는 소리로 판단해야 한다. 2는 "이" 라서 받침이 없고 1은 "일" 이라서
// 받침이 있으므로, 같은 자리에 오는 조사가 서로 다르다.

// digitEndings is whether each digit's Korean reading ends in a consonant, and
// whether that consonant is ㄹ — 로/으로 treats ㄹ like no consonant at all.
var digitEndings = map[rune]struct{ final, rieul bool }{
	'0': {true, false},  // 영
	'1': {true, true},   // 일
	'2': {false, false}, // 이
	'3': {true, false},  // 삼
	'4': {false, false}, // 사
	'5': {false, false}, // 오
	'6': {true, false},  // 육
	'7': {true, true},   // 칠
	'8': {true, true},   // 팔
	'9': {false, false}, // 구
}

// latinEndings covers the letters that end an English word said aloud in
// Korean. Only the common ones matter; anything else is treated as open.
var latinEndings = map[rune]struct{ final, rieul bool }{
	'l': {true, true}, 'L': {true, true},
	'm': {true, false}, 'M': {true, false},
	'n': {true, false}, 'N': {true, false},
	'g': {true, false}, 'G': {true, false},
	'k': {true, false}, 'K': {true, false},
	'p': {true, false}, 'P': {true, false},
	'b': {true, false}, 'B': {true, false},
	't': {true, false}, 'T': {true, false},
	'c': {true, false}, 'C': {true, false},
	's': {true, false}, 'S': {true, false},
	'x': {true, false}, 'X': {true, false},
	'z': {true, false}, 'Z': {true, false},
}

// symbolEndings are symbols read as words. Stripping them and judging by the
// digit underneath gets it wrong: "10%" is said 십 퍼센트 and takes 로, not the
// 으로 that the 0 would ask for.
var symbolEndings = map[rune]struct{ final, rieul bool }{
	'%': {false, false}, // 퍼센트
	'$': {false, false}, // 달러
	'€': {false, false}, // 유로
	'°': {false, false}, // 도
}

// wordEnding reports how the last sound of a word ends.
func wordEnding(word string) (final, rieul, known bool) {
	trimmed := strings.TrimRight(strings.TrimSpace(word), " .,)]}'\"")
	if trimmed == "" {
		return false, false, false
	}
	letters := []rune(trimmed)
	last := letters[len(letters)-1]
	if ending, ok := symbolEndings[last]; ok {
		return ending.final, ending.rieul, true
	}
	if ending, ok := digitEndings[last]; ok {
		return ending.final, ending.rieul, true
	}
	if ending, ok := latinEndings[last]; ok {
		return ending.final, ending.rieul, true
	}
	if last >= 0xAC00 && last <= 0xD7A3 {
		jongseong := (last - 0xAC00) % 28
		return jongseong != 0, jongseong == 8, true
	}
	return false, false, false
}

// withParticle appends the right form of a particle to a word. When the ending
// cannot be worked out the open form is used rather than "이(가)", because a
// wrong particle reads as a typo and a bracketed pair reads as a machine.
func withParticle(word, afterConsonant, afterVowel string) string {
	final, _, _ := wordEnding(word)
	if final {
		return word + afterConsonant
	}
	return word + afterVowel
}

// withInstrumental appends 로/으로, which treats a final ㄹ as no consonant.
func withInstrumental(word string) string {
	final, rieul, _ := wordEnding(word)
	if final && !rieul {
		return word + "으로"
	}
	return word + "로"
}
