package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("应使用 Argon2id PHC 格式：%s", h)
	}
	if !VerifyPassword(h, "correct horse") {
		t.Fatal("正确密码验证失败")
	}
	if VerifyPassword(h, "wrong horse") {
		t.Fatal("错误密码不应通过")
	}
	if VerifyPassword("garbage", "x") {
		t.Fatal("格式错误的哈希不应通过")
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Fatal("相同密码的哈希应带不同盐")
	}
}

func TestStrengthAndRandom(t *testing.T) {
	if CheckPasswordStrength("short") == nil {
		t.Fatal("过短密码应被拒绝")
	}
	if err := CheckPasswordStrength("longenough1"); err != nil {
		t.Fatal(err)
	}
	a, b := RandomPassword(16), RandomPassword(16)
	if len(a) != 16 || a == b {
		t.Fatalf("随机密码不正确：%q %q", a, b)
	}
}
