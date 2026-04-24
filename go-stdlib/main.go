package main

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

const serviceName = "go-stdlib"

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", handlePing)
	mux.HandleFunc("/fibonacci", handleFibonacci)
	mux.HandleFunc("/echo", handleEcho)
	mux.HandleFunc("/alloc", handleAlloc)
	mux.HandleFunc("/metrics", handleMetrics)
	http.ListenAndServe(":8086", mux)
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PingResponse{Status: "ok", Service: serviceName})
}

func handleFibonacci(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = 35
	}
	start := time.Now()
	result := fib(n)
	elapsed := time.Since(start).Seconds() * 1000
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FibResponse{N: n, Result: result, TimeMs: elapsed})
}

func handleEcho(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, r.Body)
}

func handleAlloc(w http.ResponseWriter, r *http.Request) {
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = 1048576
	}
	buf := make([]byte, size)
	_ = buf
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AllocResponse{
		RequestedBytes: size,
		AllocedMB:      float64(size) / 1048576.0,
	})
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(MetricsResponse{
		MemMB:   float64(ms.Alloc) / 1048576,
		Threads: runtime.NumGoroutine(),
	})
}

func fib(n int) int64 {
	if n <= 1 {
		return int64(n)
	}
	return fib(n-1) + fib(n-2)
}
