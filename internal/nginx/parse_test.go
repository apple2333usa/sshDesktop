package nginx

import (
	"strings"
	"testing"
)

const sample = `# 注释
user www-data;
http {
    include mime.types;
    server {
        listen 80;
        server_name a.test "b.test";
        location / {
            proxy_pass http://127.0.0.1:8080; # 行尾注释
        }
        content_by_lua_block {
            ngx.say("{ } ;")
        }
    }
}
`

func TestParse(t *testing.T) {
	ds, err := Parse(sample)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 2 || ds[0].Name != "user" || ds[1].Name != "http" || !ds[1].IsBlock() {
		t.Fatalf("%+v", ds)
	}
	srv := ds[1].Find("server")
	if srv == nil {
		t.Fatal("没有 server")
	}
	if sn := srv.Find("server_name"); sn == nil || strings.Join(sn.Args, ",") != "a.test,b.test" {
		t.Fatalf("server_name %+v", sn)
	}
	loc := srv.Find("location")
	if loc == nil || loc.Args[0] != "/" || loc.Find("proxy_pass").Args[0] != "http://127.0.0.1:8080" {
		t.Fatalf("location %+v", loc)
	}
	if lua := srv.Find("content_by_lua_block"); lua == nil {
		t.Fatal("lua 块应作为一条指令保留")
	}
	// 偏移量指向原文
	if got := sample[srv.Start:srv.End]; !strings.HasPrefix(got, "server") || !strings.HasSuffix(got, "}") {
		t.Fatalf("偏移量不对：%q", got)
	}
	var names []string
	Walk(ds, func(d *Directive, parents []*Directive) {
		if d.Name == "listen" {
			for _, p := range parents {
				names = append(names, p.Name)
			}
		}
	})
	if strings.Join(names, "/") != "http/server" {
		t.Fatalf("Walk 的父级 %v", names)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{"http {", "http { }}", `server_name "a;`} {
		if _, err := Parse(src); err == nil {
			t.Errorf("%q 应该解析失败", src)
		}
	}
}

func TestInsertInclude(t *testing.T) {
	out, err := InsertInclude(sample, "include /etc/nginx/conf.d/sshdesktop/*.conf;")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	inc := ds[1].All("include")
	if len(inc) != 2 || inc[1].Args[0] != "/etc/nginx/conf.d/sshdesktop/*.conf" {
		t.Fatalf("%s", out)
	}
	if _, err := InsertInclude("events {}\n", "include x;"); err != ErrNoHTTP {
		t.Fatalf("没有 http 块应返回 ErrNoHTTP，得到 %v", err)
	}
}

func TestSplitDumpAndIncluded(t *testing.T) {
	dump := "nginx: the configuration file /etc/nginx/nginx.conf syntax is ok\n" +
		"# configuration file /etc/nginx/nginx.conf:\nhttp { include /etc/nginx/conf.d/*.conf; }\n\n" +
		"# configuration file /etc/nginx/conf.d/sshdesktop.conf:\ninclude /etc/nginx/conf.d/sshdesktop/*.conf;\n"
	files := splitDump(dump)
	if len(files) != 2 || files[1].Path != "/etc/nginx/conf.d/sshdesktop.conf" || !strings.Contains(files[0].Content, "http {") {
		t.Fatalf("%+v", files)
	}
	if !Included(files, "/etc/nginx/conf.d/sshdesktop.conf") || Included(files, "/etc/nginx/other.conf") {
		t.Fatal("Included 判断错误")
	}
}

func TestAtLeast(t *testing.T) {
	cases := map[string]bool{"1.25.1": true, "1.24.0": false, "1.31.1.1": true, "1.25.0": false, "2.0": true, "": false}
	for v, want := range cases {
		if got := AtLeast(v, 1, 25, 1); got != want {
			t.Errorf("AtLeast(%q) = %v", v, got)
		}
	}
}

func TestQuote(t *testing.T) {
	if Quote("/var/www/a") != "/var/www/a" || Quote("/var/my site") != `"/var/my site"` {
		t.Fatal(Quote("/var/my site"))
	}
}
