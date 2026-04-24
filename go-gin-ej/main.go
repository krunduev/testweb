package main

import (
	"io"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const serviceName = "go-gin-ej"

func main() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/ping", handlePing)
	r.GET("/fibonacci", handleFibonacci)
	r.POST("/echo", handleEcho)
	r.GET("/alloc", handleAlloc)
	r.GET("/metrics", handleMetrics)
	r.Run(":8082")
}

func sendJSON(c *gin.Context, data []byte) {
	c.Data(http.StatusOK, "application/json", data)
}

func handlePing(c *gin.Context) {
	data, _ := (PingResponse{Status: "ok", Service: serviceName}).MarshalJSON()
	sendJSON(c, data)
}

func handleFibonacci(c *gin.Context) {
	n, _ := strconv.Atoi(c.Query("n"))
	if n <= 0 {
		n = 35
	}
	start := time.Now()
	result := fib(n)
	elapsed := time.Since(start).Seconds() * 1000
	data, _ := (FibResponse{N: n, Result: result, TimeMs: elapsed}).MarshalJSON()
	sendJSON(c, data)
}

func handleEcho(c *gin.Context) {
	body, _ := io.ReadAll(c.Request.Body)
	c.Data(http.StatusOK, "application/json", body)
}

func handleAlloc(c *gin.Context) {
	size, _ := strconv.Atoi(c.Query("size"))
	if size <= 0 {
		size = 1048576
	}
	buf := make([]byte, size)
	_ = buf
	data, _ := (AllocResponse{RequestedBytes: size, AllocedMB: float64(size) / 1048576.0}).MarshalJSON()
	sendJSON(c, data)
}

func handleMetrics(c *gin.Context) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	data, _ := (MetricsResponse{
		MemMB:   float64(ms.Alloc) / 1048576,
		Threads: runtime.NumGoroutine(),
	}).MarshalJSON()
	sendJSON(c, data)
}

func fib(n int) int64 {
	if n <= 1 {
		return int64(n)
	}
	return fib(n-1) + fib(n-2)
}
