package workbook

import "testing"

// 자기가 고친 것을 메일로 다시 받는 것은 알림이 아니라 소음이다. 그리고
// 한 번의 저장에 칸이 500개 바뀌어도 사람마다 한 통이어야 한다.
func TestWatchersAreNotifiedOncePerSaveAndNeverAboutTheirOwnEdits(t *testing.T) {
	t.Parallel()
	rules := []WatchRule{
		{Watcher: "보라", Range: "A1:B10", Label: "매출표", Enabled: true},
		{Watcher: "준", Range: "C1:C5", Enabled: true},
		{Watcher: "민", Range: "", Enabled: true},
		{Watcher: "끈사람", Range: "A1:B10", Enabled: false},
	}
	changed := []CellCoordinate{{Row: 1, Column: 1}, {Row: 2, Column: 2}, {Row: 3, Column: 3}}
	notices := WatchersToNotify(rules, "지은", changed)
	if len(notices) != 3 {
		t.Fatalf("알릴 사람=%#v", notices)
	}
	// 보라는 A1:B10 안의 두 칸이 바뀌었다.
	if notice := notices["보라"]; notice.Cells != 2 || notice.FirstCell != "A1" || notice.Label != "매출표" {
		t.Errorf("보라=%#v", notice)
	}
	// 준은 C3 하나.
	if notice := notices["준"]; notice.Cells != 1 || notice.FirstCell != "C3" {
		t.Errorf("준=%#v", notice)
	}
	// 범위를 적지 않은 민은 시트 전체를 지켜보므로 셋 다 걸린다.
	if notice := notices["민"]; notice.Cells != 3 {
		t.Errorf("민=%#v", notice)
	}
	// 꺼 둔 규칙은 알리지 않는다.
	if _, found := notices["끈사람"]; found {
		t.Error("꺼 둔 규칙이 알림을 냈다")
	}
	// 자기가 고친 것은 자기에게 알리지 않는다.
	if own := WatchersToNotify(rules, "보라", changed); len(own) != 2 {
		t.Errorf("자기 편집=%#v", own)
	}
	// 지켜보는 자리가 아니면 아무에게도 알리지 않는다.
	if none := WatchersToNotify(rules[:2], "지은", []CellCoordinate{{Row: 50, Column: 50}}); len(none) != 0 {
		t.Errorf("범위 밖=%#v", none)
	}
}

// 같은 사람이 겹치는 범위를 여럿 지켜봐도 한 통이다.
func TestOverlappingRulesForOnePersonCollapseIntoOneNotice(t *testing.T) {
	t.Parallel()
	rules := []WatchRule{
		{Watcher: "보라", Range: "A1:C10", Enabled: true},
		{Watcher: "보라", Range: "B1:B20", Enabled: true},
	}
	notices := WatchersToNotify(rules, "지은", []CellCoordinate{{Row: 1, Column: 2}})
	if len(notices) != 1 {
		t.Fatalf("알림 수=%d", len(notices))
	}
	notice := notices["보라"]
	// 두 규칙에 다 걸리므로 두 번 센다. 어느 규칙이 걸렸는지는 둘 다 적는다.
	if notice.Cells != 2 || len(notice.Ranges) != 2 {
		t.Errorf("겹치는 규칙=%#v", notice)
	}
}

// 범위를 잘못 적으면 저장하지 않는다. 저장해 두면 아무에게도 알리지 않는
// 규칙이 조용히 남는다.
func TestWatchRuleNormalisation(t *testing.T) {
	t.Parallel()
	if _, err := normalizeWatchRule(WatchRule{Watcher: "보라", Range: "여긴범위아님"}); err == nil {
		t.Error("범위가 아닌 것이 통과했다")
	}
	if _, err := normalizeWatchRule(WatchRule{Watcher: "  "}); err == nil {
		t.Error("지켜보는 사람 없이 통과했다")
	}
	// 범위를 적지 않으면 시트 전체다.
	whole, err := normalizeWatchRule(WatchRule{Watcher: "보라", Range: "  "})
	if err != nil || whole.Range != "" {
		t.Errorf("시트 전체=%#v %v", whole, err)
	}
	// 적은 범위는 한 가지 모양으로 다듬는다. b2 도 B2:B2 로 저장한다.
	single, err := normalizeWatchRule(WatchRule{Watcher: "보라", Range: "b2"})
	if err != nil || single.Range != "B2:B2" {
		t.Errorf("한 칸=%#v %v", single, err)
	}
	if !WatchRuleCovers(single, 2, 2) || WatchRuleCovers(single, 3, 2) {
		t.Error("다듬은 범위가 제 칸을 가리키지 않는다")
	}
}
