package presentation

import "testing"

// 조사가 틀리면 사람이 쓴 글이 아니라는 것이 한눈에 보인다. 숫자는 읽는
// 소리로 판단해야 한다 — 2는 "이" 라 받침이 없고 1은 "일" 이라 받침이 있다.
func TestParticlesFollowHowTheWordIsSaid(t *testing.T) {
	t.Parallel()
	for word, want := range map[string]string{
		"영업1": "영업1이", "영업2": "영업2가", "영업3": "영업3이",
		"영업4": "영업4가", "영업6": "영업6이", "영업9": "영업9가",
		"강남": "강남이", "판교": "판교가", "서울": "서울이",
		"Seoul": "Seoul이", "Data": "Data가",
	} {
		if got := withParticle(word, "이", "가"); got != want {
			t.Fatalf("withParticle(%q) = %q, want %q", word, got, want)
		}
	}
}

// 로와 으로는 ㄹ 받침을 받침 없는 것처럼 다룬다.
func TestInstrumentalParticleTreatsRieulAsOpen(t *testing.T) {
	t.Parallel()
	for word, want := range map[string]string{
		"91%":  "91%로", // 퍼센트
		"10%":  "10%로", // 십 퍼센트 — 0을 보고 판단하면 "으로" 가 된다
		"120억": "120억으로",
		"770":  "770으로", // 영
		"91":   "91로",   // 일
		"강남":   "강남으로",
		"판교":   "판교로",
	} {
		if got := withInstrumental(word); got != want {
			t.Fatalf("withInstrumental(%q) = %q, want %q", word, got, want)
		}
	}
}

// 알 수 없는 끝소리에는 "이(가)" 대신 열린 형태를 쓴다. 괄호 낀 조사는 기계가
// 쓴 티가 가장 많이 나는 자리다.
func TestUnknownEndingsDoNotProduceBracketedParticles(t *testing.T) {
	t.Parallel()
	for _, word := range []string{"?", "…", ""} {
		if got := withParticle(word, "이", "가"); got != word+"가" {
			t.Fatalf("withParticle(%q) = %q", word, got)
		}
	}
}
