// Package metric 定义监控指标结构，面板与独立 agent 共用（不依赖数据库）。
package metric

// Metric 是一个时间点（或一段时间平均）的主机指标。
type Metric struct {
	NodeID       string  `gorm:"primaryKey;size:64" json:"-"`
	TS           int64   `gorm:"primaryKey" json:"ts"` // Unix 秒
	CPU          float64 `json:"cpu"`
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
	MemUsed      uint64  `json:"memUsed"`
	MemTotal     uint64  `json:"memTotal"`
	SwapUsed     uint64  `json:"swapUsed"`
	SwapTotal    uint64  `json:"swapTotal"`
	DiskUsed     uint64  `json:"diskUsed"`
	DiskTotal    uint64  `json:"diskTotal"`
	DiskReadBps  float64 `json:"diskReadBps"`
	DiskWriteBps float64 `json:"diskWriteBps"`
	NetRxBps     float64 `json:"netRxBps"`
	NetTxBps     float64 `json:"netTxBps"`
	TCPConns     float64 `json:"tcpConns"`
	Procs        float64 `json:"procs"`
}
