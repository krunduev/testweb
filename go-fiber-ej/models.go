package main

type PingResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

type FibResponse struct {
	N      int     `json:"n"`
	Result int64   `json:"result"`
	TimeMs float64 `json:"time_ms"`
}

type AllocResponse struct {
	RequestedBytes int     `json:"requested_bytes"`
	AllocedMB      float64 `json:"alloced_mb"`
}

type MetricsResponse struct {
	MemMB   float64 `json:"mem_mb"`
	Threads int     `json:"threads"`
}
