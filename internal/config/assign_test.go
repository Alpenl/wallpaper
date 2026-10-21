package config

import "testing"

func TestAssignmentsAlias(t *testing.T) {
	var a Assignments
	if got := a.SetAlias("d1", "  客厅\t大屏\n"); got != "客厅大屏" || a.Aliases["d1"] != "客厅大屏" {
		t.Fatalf("%q %+v", got, a.Aliases)
	}
	long := ""
	for i := 0; i < 40; i++ {
		long += "屏"
	}
	if got := a.SetAlias("d2", long); len([]rune(got)) != MaxAliasLen {
		t.Fatal(len([]rune(got)))
	}
	a.SetAlias("d1", "   ")
	if _, ok := a.Aliases["d1"]; ok {
		t.Fatal("空白应恢复默认名")
	}
}

func TestAssignmentsMigrate(t *testing.T) {
	a := Assignments{Displays: map[string]string{"old": "x", "new2": "keep", "old2": "drop"}, Aliases: map[string]string{"old": "左"}}
	if !a.Migrate("new", []string{"old"}) {
		t.Fatal("应有改动")
	}
	if a.Displays["new"] != "x" || a.Aliases["new"] != "左" || a.Displays["old"] != "" || a.Aliases["old"] != "" {
		t.Fatalf("%+v", a)
	}
	a.Migrate("new2", []string{"old2"}) // 新 ID 已有设置：不覆盖，旧键清掉
	if a.Displays["new2"] != "keep" || a.Displays["old2"] != "" {
		t.Fatalf("%+v", a)
	}
	if a.Migrate("new", []string{"none", "new"}) {
		t.Fatal("没有旧键时不应报改动")
	}
}
