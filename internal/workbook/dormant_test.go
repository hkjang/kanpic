package workbook

import (
	"context"
	"testing"
)

// 무엇이 잠들어 있는지 알아야 정리할지 남길지 정할 수 있다. 한 번도 들어온
// 적 없는 계정도 잠든 것으로 센다 — 미리 등록해 두고 아무도 쓰지 않은
// 계정이 그대로 남는 일이 흔하다.
func TestOverviewCountsWhatHasGoneQuiet(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()

	for _, id := range []string{"never.lee", "also.never"} {
		if _, err := repository.UpsertUser(ctx, UpsertUserInput{UserID: id, DisplayName: id}); err != nil {
			t.Fatal(err)
		}
	}
	overview, err := repository.AdminOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if overview.DormantUsers != 2 {
		t.Errorf("잠든 계정 = %d, 한 번도 들어온 적 없는 계정 둘이어야 한다", overview.DormantUsers)
	}
}

// 오래 손대지 않은 워크북만 걸러 낸다.
func TestDormantFilterKeepsOnlyOldWorkbooks(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()
	if _, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "올해 자료", OwnerID: "kim"}); err != nil {
		t.Fatal(err)
	}
	items, err := repository.GovernedWorkbooks(ctx, GovernanceFilterDormant, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("방금 만든 워크북이 잠든 것으로 나온다: %#v", items)
	}
	// 거르개 이름 자체가 받아들여져야 한다. 서버가 모르는 이름이면 화면의
	// 탭이 조용히 빈 목록을 보여 준다.
	if !ValidGovernanceFilter(GovernanceFilterDormant) {
		t.Error("dormant 거르개를 모른다")
	}
}

// 부서가 여러 겹이어도 위 부서를 맡은 사람이 아래 구성원을 본다. 부서가
// 나뉘어도 맡은 사람이 바뀌지 않아야 하기 때문이다.
func TestManagedMembersReachDownTheTree(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()
	top, err := repository.CreateDepartment(ctx, CreateDepartmentInput{Name: "본부"})
	if err != nil {
		t.Fatal(err)
	}
	middle, err := repository.CreateDepartment(ctx, CreateDepartmentInput{Name: "실", ParentID: top.ID})
	if err != nil {
		t.Fatal(err)
	}
	bottom, err := repository.CreateDepartment(ctx, CreateDepartmentInput{Name: "팀", ParentID: middle.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AddDepartmentMembers(ctx, bottom.ID, DepartmentMembersInput{UserIDs: []string{"deep.kim"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AddDepartmentManagers(ctx, top.ID, DepartmentMembersInput{UserIDs: []string{"lead.park"}}); err != nil {
		t.Fatal(err)
	}
	members, err := repository.ManagedMembers(ctx, "lead.park")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0] != "deep.kim" {
		t.Errorf("맡은 구성원 = %#v", members)
	}
	// 맡지 않은 사람에게는 아무것도 없다.
	if others, err := repository.ManagedMembers(ctx, "nobody"); err != nil || len(others) != 0 {
		t.Errorf("맡지 않은 사람 = %#v err=%v", others, err)
	}
}
