package agent

import (
	"os/exec"
	"strings"
	"testing"
)

// 在本机跑两轮采集脚本，检查解析结果大致合理。
func TestProcSamplerLocal(t *testing.T) {
	script := strings.Replace(SampleScript, "sleep 5", "sleep 1", 1)
	script = strings.Replace(script, "while :; do", "for i in 1 2; do", 1)
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Skip("无法运行 sh：", err)
	}
	blocks := strings.Split(string(out), "@@END")
	var p ProcSampler
	if s := p.Parse(blocks[0]); s != nil {
		t.Fatal("第一轮应只建立基线")
	}
	s := p.Parse(blocks[1])
	if s == nil {
		t.Fatal("第二轮没有结果")
	}
	if s.MemTotal == 0 || s.MemUsed == 0 || s.MemUsed > s.MemTotal {
		t.Errorf("内存不合理：%d / %d", s.MemUsed, s.MemTotal)
	}
	if s.CPU < 0 || s.CPU > 100 || len(s.CPUCores) == 0 {
		t.Errorf("CPU 不合理：%v %v", s.CPU, s.CPUCores)
	}
	if s.Procs < 1 || s.Uptime == 0 || s.DiskTotal == 0 {
		t.Errorf("进程数、运行时间或磁盘不合理：%+v", s)
	}

	info, _ := exec.Command("sh", "-c", InfoScript).Output()
	in := ParseInfo(string(info))
	if in.Hostname == "" || in.Kernel == "" || in.Arch == "" || in.CPUThreads == 0 {
		t.Errorf("主机信息不完整：%+v", in)
	}
}
