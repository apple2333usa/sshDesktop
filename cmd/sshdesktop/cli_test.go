package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/auth"
	"github.com/apple2333usa/sshdesktop/internal/rbac"
	"github.com/apple2333usa/sshdesktop/internal/store"
)

func TestDeleteUser(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := rbac.Seed(st.DB); err != nil {
		t.Fatal(err)
	}
	var admin store.Role
	st.DB.Where("name = ?", rbac.AdminRoleName).First(&admin)
	a, err := auth.CreateUser(st.DB, "admin", "Passw0rd!x", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.CreateUser(st.DB, "m4test", "Passw0rd!x", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	st.DB.Create(&store.Session{TokenHash: "x", UserID: b.ID})
	st.DB.Create(&store.DesktopState{UserID: b.ID, State: "{}"})

	if err := deleteUser(st, "nobody"); err == nil {
		t.Fatal("不存在的账号应该报错")
	}
	if err := deleteUser(st, "m4test"); err != nil {
		t.Fatal(err)
	}
	var n int64
	st.DB.Model(&store.User{}).Where("id = ?", b.ID).Count(&n)
	if n != 0 {
		t.Fatal("账号没有删除")
	}
	st.DB.Model(&store.Session{}).Where("user_id = ?", b.ID).Count(&n)
	if n != 0 {
		t.Fatal("会话没有注销")
	}
	st.DB.Model(&store.DesktopState{}).Where("user_id = ?", b.ID).Count(&n)
	if n != 0 {
		t.Fatal("桌面布局没有删除")
	}
	// 最后一个启用中的管理员不能删除
	if err := deleteUser(st, "admin"); err == nil {
		t.Fatal("最后一个管理员不应被删除")
	}
	st.DB.Model(&store.User{}).Where("id = ?", a.ID).Count(&n)
	if n != 1 {
		t.Fatal("管理员被删除了")
	}
}

func TestPruneUpgradeBackups(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	names := []string{
		"sshdesktop-0.1.0-20261020-100000", "sshdesktop-0.2.0-20261020-200000", "sshdesktop-0.3.0-20261021-000000",
		"sshdesktop-0.4.0-20261021-140000", "sshdesktop-0.4.1-20261021-160000",
		"sshdesktop-20261020-100000.db", "sshdesktop-20261020-200000.db", "sshdesktop-20261021-000000.db",
		"sshdesktop-20261021-140000.db", "other.txt",
	}
	for i, n := range names {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte("x"), 0o600)
		mt := base.Add(time.Duration(i%5) * time.Minute)
		os.Chtimes(p, mt, mt)
	}
	if n := pruneUpgradeBackups(dir, 3); n != 3 {
		t.Fatalf("删除了 %d 个，期望 3 个", n)
	}
	left, _ := os.ReadDir(dir)
	var got []string
	for _, e := range left {
		got = append(got, e.Name())
	}
	want := "other.txt sshdesktop-0.3.0-20261021-000000 sshdesktop-0.4.0-20261021-140000 sshdesktop-0.4.1-20261021-160000 " +
		"sshdesktop-20261020-200000.db sshdesktop-20261021-000000.db sshdesktop-20261021-140000.db"
	if strings.Join(got, " ") != want {
		t.Fatalf("剩下：%v", got)
	}
}
