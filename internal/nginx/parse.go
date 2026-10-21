// Package nginx 解析和管理 Nginx / OpenResty：宿主机已有的 Nginx（接管）或面板安装的 OpenResty 容器。
package nginx

import (
	"fmt"
	"strings"
)

// Directive 是配置中的一条指令；Block 不为 nil 表示带 {} 块。Start、End 为在源文本中的字节偏移（End 指向 ; 或 } 之后）。
type Directive struct {
	Name  string       `json:"name"`
	Args  []string     `json:"args"`
	Block []*Directive `json:"block,omitempty"`
	Line  int          `json:"line"`
	Start int          `json:"-"`
	End   int          `json:"-"`
	// BlockOpen、BlockClose 是 { 之后和 } 所在的偏移，用于在块内插入内容
	BlockOpen  int `json:"-"`
	BlockClose int `json:"-"`
}

func (d *Directive) IsBlock() bool { return d.Block != nil }

// Find 返回块内第一条名为 name 的指令。
func (d *Directive) Find(name string) *Directive {
	for _, c := range d.Block {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// All 返回块内所有名为 name 的指令。
func (d *Directive) All(name string) []*Directive {
	var out []*Directive
	for _, c := range d.Block {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

type token struct {
	s      string
	quoted bool
	start  int
	end    int
	line   int
}

func tokenize(src string) ([]token, error) {
	var out []token
	line := 1
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '{' || c == '}' || c == ';':
			out = append(out, token{s: string(c), start: i, end: i + 1, line: line})
			i++
		case c == '"' || c == '\'':
			q := c
			start, l := i, line
			i++
			var b strings.Builder
			for i < len(src) && src[i] != q {
				if src[i] == '\\' && i+1 < len(src) {
					b.WriteByte(src[i+1])
					i += 2
					continue
				}
				if src[i] == '\n' {
					line++
				}
				b.WriteByte(src[i])
				i++
			}
			if i >= len(src) {
				return nil, fmt.Errorf("第 %d 行：引号没有闭合", l)
			}
			i++
			out = append(out, token{s: b.String(), quoted: true, start: start, end: i, line: l})
		default:
			start := i
			var b strings.Builder
			for i < len(src) {
				c := src[i]
				if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ';' || c == '{' || c == '}' {
					break
				}
				// ${var} 中的大括号属于参数
				if c == '$' && i+1 < len(src) && src[i+1] == '{' {
					j := strings.IndexByte(src[i:], '}')
					if j > 0 {
						b.WriteString(src[i : i+j+1])
						i += j + 1
						continue
					}
				}
				if c == '\\' && i+1 < len(src) {
					b.WriteByte(c)
					b.WriteByte(src[i+1])
					i += 2
					continue
				}
				b.WriteByte(c)
				i++
			}
			out = append(out, token{s: b.String(), start: start, end: i, line: line})
		}
	}
	return out, nil
}

// Parse 解析配置文本。*_by_lua_block 等 Lua 块的内容不按 Nginx 语法解析，只记录位置。
func Parse(src string) ([]*Directive, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	pos := 0
	var parse func(depth int) ([]*Directive, error)
	parse = func(depth int) ([]*Directive, error) {
		out := []*Directive{}
		for pos < len(toks) {
			t := toks[pos]
			if t.s == "}" && !t.quoted {
				if depth == 0 {
					return nil, fmt.Errorf("第 %d 行：多余的 }", t.line)
				}
				return out, nil
			}
			if (t.s == "{" || t.s == ";") && !t.quoted {
				return nil, fmt.Errorf("第 %d 行：缺少指令名", t.line)
			}
			d := &Directive{Name: t.s, Line: t.line, Start: t.start}
			pos++
			for pos < len(toks) {
				a := toks[pos]
				if !a.quoted && (a.s == ";" || a.s == "{" || a.s == "}") {
					break
				}
				d.Args = append(d.Args, a.s)
				pos++
			}
			if pos >= len(toks) {
				return nil, fmt.Errorf("第 %d 行：%s 缺少 ; 或 {", t.line, d.Name)
			}
			end := toks[pos]
			switch end.s {
			case ";":
				d.End = end.end
				pos++
			case "}":
				return nil, fmt.Errorf("第 %d 行：%s 缺少 ;", t.line, d.Name)
			case "{":
				d.BlockOpen = end.end
				if strings.HasSuffix(d.Name, "_by_lua_block") {
					// 跳过 Lua 代码：按大括号配对
					close, err := skipLua(src, end.end)
					if err != nil {
						return nil, fmt.Errorf("第 %d 行：%w", t.line, err)
					}
					d.Block = []*Directive{}
					d.BlockClose, d.End = close, close+1
					for pos < len(toks) && toks[pos].start <= close {
						pos++
					}
					break
				}
				pos++
				block, err := parse(depth + 1)
				if err != nil {
					return nil, err
				}
				if pos >= len(toks) {
					return nil, fmt.Errorf("第 %d 行：%s 的 { 没有闭合", t.line, d.Name)
				}
				d.Block = block
				d.BlockClose, d.End = toks[pos].start, toks[pos].end
				pos++
			}
			out = append(out, d)
		}
		if depth > 0 {
			return nil, fmt.Errorf("缺少 }")
		}
		return out, nil
	}
	return parse(0)
}

func skipLua(src string, from int) (int, error) {
	depth := 1
	for i := from; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		case '"', '\'':
			q := src[i]
			for i++; i < len(src) && src[i] != q; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case '-':
			if i+1 < len(src) && src[i+1] == '-' {
				for i < len(src) && src[i] != '\n' {
					i++
				}
			}
		}
	}
	return 0, fmt.Errorf("Lua 块没有闭合")
}

// Walk 深度优先遍历所有指令。
func Walk(ds []*Directive, fn func(d *Directive, parents []*Directive)) {
	var walk func(ds []*Directive, parents []*Directive)
	walk = func(ds []*Directive, parents []*Directive) {
		for _, d := range ds {
			fn(d, parents)
			if d.Block != nil {
				walk(d.Block, append(parents, d))
			}
		}
	}
	walk(ds, nil)
}

// Quote 在需要时给参数加引号。
func Quote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\n;{}\"'#") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return s
}
