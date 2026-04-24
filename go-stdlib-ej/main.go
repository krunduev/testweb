package main

import (
	"io"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

const serviceName = "go-stdlib-ej"

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", handlePing)
	mux.HandleFunc("/fibonacci", handleFibonacci)
	mux.HandleFunc("/echo", handleEcho)
	mux.HandleFunc("/alloc", handleAlloc)
	mux.HandleFunc("/metrics", handleMetrics)
	http.ListenAndServe(":8081", mux)
}

func writeJSON(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	data, _ := (PingResponse{Status: "ok", Service: serviceName}).MarshalJSON()
	writeJSON(w, data)
}

func handleFibonacci(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = 35
	}
	start := time.Now()
	result := fib(n)
	elapsed := time.Since(start).Seconds() * 1000
	data, _ := (FibResponse{N: n, Result: result, TimeMs: elapsed}).MarshalJSON()
	writeJSON(w, data)
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
	data, _ := (AllocResponse{RequestedBytes: size, AllocedMB: float64(size) / 1048576.0}).MarshalJSON()
	writeJSON(w, data)
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	data, _ := (MetricsResponse{
		MemMB:   float64(ms.Alloc) / 1048576,
		Threads: runtime.NumGoroutine(),
	}).MarshalJSON()
	writeJSON(w, data)
}

func fib(n int) int64 {
	if n <= 1 {
		return int64(n)
	}
	return fib(n-1) + fib(n-2)
}
