package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/store"
)

func pair(t *testing.T, names ...string) ([]byte, []byte) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, Issuer: pkix.Name{CommonName: "Test CA"},
		DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
}

func TestParsePair(t *testing.T) {
	c, k := pair(t, "a.test", "*.a.test")
	info, err := ParsePair(c, k)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Domains) != 2 || info.Domains[1] != "*.a.test" || info.NotAfter.Before(time.Now()) {
		t.Fatalf("%+v", info)
	}
	_, k2 := pair(t, "b.test")
	if _, err := ParsePair(c, k2); err == nil {
		t.Fatal("证书和私钥不匹配应报错")
	}
	if _, err := ParsePair([]byte("x"), k); err == nil {
		t.Fatal("格式错误应报错")
	}
}

func TestRenewDue(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	day := 24 * time.Hour
	cases := []struct {
		c    store.Certificate
		want bool
	}{
		{store.Certificate{AutoRenew: true, NotBefore: at(-60 * day), NotAfter: at(29 * day)}, true},
		{store.Certificate{AutoRenew: true, NotBefore: at(-59 * day), NotAfter: at(31 * day)}, false},
		{store.Certificate{AutoRenew: false, NotBefore: at(-89 * day), NotAfter: at(1 * day)}, false},
		{store.Certificate{AutoRenew: true}, false},
		// 45 天的短期证书：剩余 15 天时续期
		{store.Certificate{AutoRenew: true, NotBefore: at(-29 * day), NotAfter: at(16 * day)}, false},
		{store.Certificate{AutoRenew: true, NotBefore: at(-31 * day), NotAfter: at(14 * day)}, true},
	}
	for i, c := range cases {
		if got := renewDue(c.c, now); got != c.want {
			t.Errorf("第 %d 个：%v", i, got)
		}
	}
}
