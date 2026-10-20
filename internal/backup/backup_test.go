package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/secret"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"golang.org/x/net/webdav"
)

func TestCryptRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 15, 16, 17, 100000, 1 << 20} {
		plain := make([]byte, n)
		rand.Read(plain)
		var buf bytes.Buffer
		w, err := NewEncrypter(&buf, "密码 pass")
		if err != nil {
			t.Fatal(err)
		}
		// 分块写入，覆盖缓冲逻辑
		for i := 0; i < n; i += 7777 {
			w.Write(plain[i:min(i+7777, n)])
		}
		w.Close()
		if buf.Len()%16 != 0 || buf.Len() < n+16+1 {
			t.Fatalf("n=%d: 密文长度 %d 不对", n, buf.Len())
		}
		r, err := NewDecrypter(bytes.NewReader(buf.Bytes()), "密码 pass")
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("n=%d: 解密结果不一致", n)
		}
		r, _ = NewDecrypter(bytes.NewReader(buf.Bytes()), "wrong")
		if _, err := io.ReadAll(r); !errors.Is(err, ErrBadPassword) && n > 0 {
			// 错误密码几乎总会导致填充校验失败（1/256 的概率碰巧通过，换成内容校验在 gzip 层）
			t.Logf("n=%d: 错误密码未在填充层发现：%v", n, err)
		}
	}
}

// TestCryptOpenSSL 确认文档中的 openssl 命令可以解密，且面板能解密 openssl 加密的文件。
func TestCryptOpenSSL(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("没有 openssl")
	}
	dir := t.TempDir()
	plain := []byte(strings.Repeat("sshDesktop 备份 ", 5000))
	var buf bytes.Buffer
	w, _ := NewEncrypter(&buf, "s3cret-pass")
	w.Write(plain)
	w.Close()
	enc := filepath.Join(dir, "a.enc")
	os.WriteFile(enc, buf.Bytes(), 0o600)
	out := filepath.Join(dir, "a.out")
	cmd := exec.Command("openssl", "enc", "-d", "-aes-256-cbc", "-pbkdf2", "-iter", "100000", "-md", "sha256",
		"-in", enc, "-out", out, "-pass", "pass:s3cret-pass")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("openssl 解密失败：%v %s", err, b)
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, plain) {
		t.Fatal("openssl 解密结果不一致")
	}
	src := filepath.Join(dir, "b.txt")
	os.WriteFile(src, plain, 0o600)
	cmd = exec.Command("openssl", "enc", "-aes-256-cbc", "-pbkdf2", "-iter", "100000", "-md", "sha256",
		"-in", src, "-out", src+".enc", "-pass", "pass:s3cret-pass")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("openssl 加密失败：%v %s", err, b)
	}
	f, _ := os.Open(src + ".enc")
	defer f.Close()
	r, err := NewDecrypter(f, "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(r); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("解密 openssl 文件失败：%v", err)
	}
}

func TestExcluder(t *testing.T) {
	ex := ParseExcludes("node_modules\n*.log\n/cache/tmp/\n\n")
	cases := map[string]bool{
		"node_modules":           true,
		"a/node_modules/x.js":    true,
		"logs/app.log":           true,
		"cache/tmp":              true,
		"cache/tmp/a":            true,
		"cache/tmpx":             false,
		"src/main.go":            false,
		"app.log.1":              false,
		"deep/cache/tmp/ignored": false,
	}
	for p, want := range cases {
		if got := ex.Match(p); got != want {
			t.Errorf("Match(%q)=%v，期望 %v", p, got, want)
		}
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.Type()&os.ModeSymlink != 0 {
			l, _ := os.Readlink(p)
			out[filepath.ToSlash(rel)] = "-> " + l
			return nil
		}
		b, _ := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	return out
}

func TestArchiveExtract(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"a.txt": "A", "sub/b.txt": "B", "node_modules/x": "X", "sub/c.log": "C"})
	os.Symlink("a.txt", filepath.Join(src, "link"))
	var buf bytes.Buffer
	tw, gz, err := newTar(&buf, Manifest{Format: 1, Type: "dir", Source: src})
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.addDir(context.Background(), src, "data", ParseExcludes("node_modules\n*.log"), t.Logf); err != nil {
		t.Fatal(err)
	}
	tw.close(gz)
	m, err := readManifest(bytes.NewReader(buf.Bytes()))
	if err != nil || m.Source != src {
		t.Fatalf("清单：%+v %v", m, err)
	}
	dst := t.TempDir()
	_, n, err := extract(bytes.NewReader(buf.Bytes()), dst, "data")
	if err != nil {
		t.Fatal(err)
	}
	got := readTree(t, dst)
	want := map[string]string{"a.txt": "A", "sub/b.txt": "B", "link": "-> a.txt"}
	if n != 2 || len(got) != len(want) {
		t.Fatalf("解压结果 %v（%d 个文件）", got, n)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q，期望 %q", k, got[k], v)
		}
	}
	if fi, _ := os.Stat(filepath.Join(dst, "a.txt")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("权限 %v", fi.Mode().Perm())
	}
}

// 构造包含 ../ 的恶意包，解压必须拒绝。
func TestExtractRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw, gz, _ := newTar(&buf, Manifest{Format: 1, Type: "dir"})
	tw.file("data/../../evil", []byte("x"), 0o644)
	tw.close(gz)
	dst := t.TempDir()
	if _, _, err := extract(bytes.NewReader(buf.Bytes()), filepath.Join(dst, "in"), "data"); err == nil {
		t.Fatal("应拒绝包外路径")
	}
	if _, err := os.Stat(filepath.Join(dst, "evil")); err == nil {
		t.Fatal("写到了包外")
	}
}

func testStorage(t *testing.T, st Storage) {
	t.Helper()
	ctx := context.Background()
	if list, err := st.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("空存储：%v %v", list, err)
	}
	body := strings.Repeat("x", 3000)
	for _, k := range []string{"plan-1/a.tar.gz", "plan-1/b.tar.gz.enc", "plan-2/中文 名.tar.gz"} {
		if err := st.Put(ctx, k, strings.NewReader(body), int64(len(body))); err != nil {
			t.Fatalf("Put %s：%v", k, err)
		}
	}
	st.Put(ctx, ".sshdesktop-test", strings.NewReader("t"), 1)
	list, err := st.List(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("List：%+v %v", list, err)
	}
	for _, o := range list {
		if o.Size != 3000 {
			t.Fatalf("大小：%+v", o)
		}
	}
	rc, err := st.Get(ctx, "plan-2/中文 名.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != body {
		t.Fatal("Get 内容不一致")
	}
	if err := st.Delete(ctx, "plan-1/a.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "plan-1/a.tar.gz"); err != nil {
		t.Fatalf("删除不存在的文件应成功：%v", err)
	}
	st.Delete(ctx, ".sshdesktop-test")
	if list, _ := st.List(ctx); len(list) != 2 {
		t.Fatalf("删除后：%+v", list)
	}
	for _, bad := range []string{"../x.tar.gz", "/abs.tar.gz", "a//b.tar.gz", ""} {
		if err := st.Put(ctx, bad, strings.NewReader("x"), 1); err == nil {
			t.Fatalf("应拒绝路径 %q", bad)
		}
	}
}

func TestLocalStorage(t *testing.T) {
	st, err := Open(context.Background(), "local", TargetConfig{Path: filepath.Join(t.TempDir(), "bk")}, TargetSecret{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, st)
	if _, err := Open(context.Background(), "local", TargetConfig{Path: "/"}, TargetSecret{}, nil); err == nil {
		t.Fatal("应拒绝根目录")
	}
}

func TestWebDAVStorage(t *testing.T) {
	dir := t.TempDir()
	h := &webdav.Handler{FileSystem: webdav.Dir(dir), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(httpBasic("u", "p", h))
	defer srv.Close()
	st, err := NewWebDAV(TargetConfig{URL: srv.URL + "/dav root/", Path: "backups/x"}, TargetSecret{})
	if err == nil {
		// 没有密码时服务器返回 401
		if _, err := st.List(context.Background()); err == nil {
			t.Fatal("没有密码应失败")
		}
	}
	os.MkdirAll(filepath.Join(dir, "dav root"), 0o755)
	st, err = NewWebDAV(TargetConfig{URL: srv.URL + "/dav root/", Path: "backups/x", Username: "u"}, TargetSecret{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, st)
}

func newEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sshdesktop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	key := make([]byte, 32)
	rand.Read(key)
	os.WriteFile(filepath.Join(dir, "secret.key"), key, 0o600)
	box, _ := secret.Load(filepath.Join(dir, "secret.key"))
	return &Engine{Store: st, Box: box, DataDir: dir, Version: "test",
		Remote: func(ctx context.Context, id string) (vfs.FS, func(), error) { return vfs.Local{}, nil, nil }}
}

func addTarget(t *testing.T, e *Engine, typ string, cfg TargetConfig) store.BackupTarget {
	b, _ := json.Marshal(cfg)
	row := store.BackupTarget{Name: typ, Type: typ, Config: string(b)}
	e.Store.DB.Create(&row)
	return row
}

func TestRunPlanRetentionRestore(t *testing.T) {
	e := newEngine(t)
	src := filepath.Join(t.TempDir(), "site")
	writeTree(t, src, map[string]string{"index.html": "v1", "img/a.png": "PNG", "tmp/cache": "skip"})
	t1 := addTarget(t, e, "local", TargetConfig{Path: filepath.Join(t.TempDir(), "t1")})
	t2 := addTarget(t, e, "sftp", TargetConfig{Path: filepath.Join(t.TempDir(), "t2"), NodeID: "n1"})
	plan := store.BackupPlan{Name: "网站", Type: "dir", Source: src, Excludes: "tmp", KeepCount: 2,
		TargetIDs: joinUint(t1.ID, t2.ID), Encrypt: true, Password: e.Box.Seal([]byte("backup-pass"))}
	e.Store.DB.Create(&plan)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		var log bytes.Buffer
		if err := e.RunPlan(ctx, plan.ID, "manual", &log); err != nil {
			t.Fatalf("备份失败：%v\n%s", err, log.String())
		}
		time.Sleep(1100 * time.Millisecond) // 文件名精确到秒
	}
	var recs []store.BackupRecord
	e.Store.DB.Where("status = ?", "success").Order("started_at").Find(&recs)
	if len(recs) != 4 { // 每个目标保留 2 份
		t.Fatalf("保留策略后应剩 4 条记录，实际 %d", len(recs))
	}
	st, _ := e.OpenTarget(ctx, t1)
	objs, _ := st.List(ctx)
	if len(objs) != 2 || !strings.HasSuffix(objs[0].Key, ".tar.gz.enc") {
		t.Fatalf("目标中的文件：%+v", objs)
	}
	// 修改源目录后从最新备份恢复：内容回到备份时，排除的目录不在备份中
	writeTree(t, src, map[string]string{"index.html": "v2", "new.txt": "new"})
	last := recs[len(recs)-1]
	var log bytes.Buffer
	res, err := e.Restore(ctx, RestoreRequest{RecordID: last.ID}, &log)
	if err != nil {
		t.Fatalf("恢复失败：%v\n%s", err, log.String())
	}
	got := readTree(t, src)
	if len(got) != 2 || got["index.html"] != "v1" || got["img/a.png"] != "PNG" {
		t.Fatalf("恢复结果：%v", got)
	}
	if res.Snapshot == "" {
		t.Fatal("应生成恢复前快照")
	}
	// 用快照撤销恢复
	var snap store.BackupRecord
	e.Store.DB.Where(map[string]any{"trigger": "snapshot"}).First(&snap)
	if _, err := e.Restore(ctx, RestoreRequest{RecordID: snap.ID, NoSnapshot: true}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readTree(t, src); got["index.html"] != "v2" || got["new.txt"] != "new" || got["tmp/cache"] != "skip" {
		t.Fatalf("快照恢复结果：%v", got)
	}
	// 恢复到其他目录；错误密码
	other := filepath.Join(t.TempDir(), "other")
	if _, err := e.Restore(ctx, RestoreRequest{TargetID: t1.ID, Key: last.Key, Password: "wrong", Dest: other}, nil); err == nil {
		t.Fatal("错误密码应失败")
	}
	if _, err := e.Restore(ctx, RestoreRequest{TargetID: t1.ID, Key: last.Key, Password: "backup-pass", Dest: other}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readTree(t, other); got["index.html"] != "v1" {
		t.Fatalf("恢复到其他目录：%v", got)
	}
	if _, err := e.Restore(ctx, RestoreRequest{RecordID: last.ID, Dest: "/etc"}, nil); err == nil {
		t.Fatal("应拒绝恢复到系统目录")
	}
}

func joinUint(ids ...uint) string {
	var s []string
	for _, id := range ids {
		s = append(s, itoa(id))
	}
	return strings.Join(s, ",")
}

func itoa(n uint) string { return strconv.FormatUint(uint64(n), 10) }

func httpBasic(user, pass string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func TestPanelBackupRestore(t *testing.T) {
	e := newEngine(t)
	cfgPath := filepath.Join(e.DataDir, "config.yaml")
	os.WriteFile(cfgPath, []byte("port: 1\n"), 0o600)
	e.ConfigPath = cfgPath
	writeTree(t, filepath.Join(e.DataDir, "compose", "p1"), map[string]string{"docker-compose.yml": "services: {}"})
	e.Store.SetSetting("marker", "before")
	tg := addTarget(t, e, "local", TargetConfig{Path: filepath.Join(t.TempDir(), "t")})
	plan := store.BackupPlan{Name: "面板", Type: "panel", TargetIDs: itoa(tg.ID)}
	e.Store.DB.Create(&plan)
	ctx := context.Background()
	if err := e.RunPlan(ctx, plan.ID, "manual", nil); err != nil {
		t.Fatal(err)
	}
	var rec store.BackupRecord
	e.Store.DB.Where("status = ?", "success").First(&rec)
	e.Store.SetSetting("marker", "after")
	res, err := e.Restore(ctx, RestoreRequest{RecordID: rec.ID}, nil)
	if err != nil || !res.NeedRestart {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Warning != "" {
		t.Fatalf("同一主密钥不应警告：%s", res.Warning)
	}
	// 模拟重启：关闭数据库后应用等待中的恢复
	e.Store.Close()
	os.WriteFile(cfgPath, []byte("port: 2\n"), 0o600)
	if err := ApplyPendingPanelRestore(e.DataDir, cfgPath); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(e.DataDir, "sshdesktop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var v string
	st.GetSetting("marker", &v)
	if v != "before" {
		t.Fatalf("恢复后的设置：%q", v)
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != "port: 2\n" {
		t.Fatal("不应覆盖当前配置")
	}
	if b, _ := os.ReadFile(cfgPath + ".from-backup"); string(b) != "port: 1\n" {
		t.Fatal("应保存备份中的配置供参考")
	}
	if _, err := os.Stat(PendingDir(e.DataDir)); err == nil {
		t.Fatal("等待目录应已删除")
	}
	matches, _ := filepath.Glob(filepath.Join(e.DataDir, "restore-old-*", "sshdesktop.db"))
	if len(matches) != 1 {
		t.Fatal("原数据库应移到 restore-old-*")
	}
}

// TestS3Storage 需要一个 S3 兼容服务，例如：
//
//	docker run -d -p 127.0.0.1:19000:8080 --entrypoint sh rclone/rclone -c \
//	  "mkdir -p /data/bkt && rclone serve s3 --auth-key testak,testsecretkey --addr :8080 /data"
//	SSHD_TEST_S3=http://127.0.0.1:19000,bkt,testak,testsecretkey go test ./internal/backup -run S3
func TestS3Storage(t *testing.T) {
	v := os.Getenv("SSHD_TEST_S3")
	if v == "" {
		t.Skip("未设置 SSHD_TEST_S3")
	}
	p := strings.Split(v, ",")
	st, err := NewS3(TargetConfig{Endpoint: p[0], Bucket: p[1], AccessKey: p[2], PathStyle: true, Region: "us-east-1",
		Path: "sshd-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)}, TargetSecret{SecretKey: p[3]})
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, st)
	bad, _ := NewS3(TargetConfig{Endpoint: p[0], Bucket: p[1], AccessKey: p[2], PathStyle: true, Region: "us-east-1"}, TargetSecret{SecretKey: "wrong"})
	if _, err := bad.List(context.Background()); err == nil {
		t.Fatal("错误的密钥应失败")
	}
}
