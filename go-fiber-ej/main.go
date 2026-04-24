package main

import (
	"runtime"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

const serviceName = "go-fiber-ej"

func main() {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/ping", handlePing)
	app.Get("/fibonacci", handleFibonacci)
	app.Post("/echo", handleEcho)
	app.Get("/alloc", handleAlloc)
	app.Get("/metrics", handleMetrics)
	app.Listen(":8083")
}

func sendJSON(c *fiber.Ctx, data []byte) error {
	c.Set("Content-Type", "application/json")
	return c.Send(data)
}

func handlePing(c *fiber.Ctx) error {
	data, _ := (PingResponse{Status: "ok", Service: serviceName}).MarshalJSON()
	return sendJSON(c, data)
}

func handleFibonacci(c *fiber.Ctx) error {
	n, _ := strconv.Atoi(c.Query("n"))
	if n <= 0 {
		n = 35
	}
	start := time.Now()
	result := fib(n)
	elapsed := time.Since(start).Seconds() * 1000
	data, _ := (FibResponse{N: n, Result: result, TimeMs: elapsed}).MarshalJSON()
	return sendJSON(c, data)
}

func handleEcho(c *fiber.Ctx) error {
	c.Set("Content-Type", "application/json")
	return c.Send(c.Body())
}

func handleAlloc(c *fiber.Ctx) error {
	size, _ := strconv.Atoi(c.Query("size"))
	if size <= 0 {
		size = 1048576
	}
	buf := make([]byte, size)
	_ = buf
	data, _ := (AllocResponse{RequestedBytes: size, AllocedMB: float64(size) / 1048576.0}).MarshalJSON()
	return sendJSON(c, data)
}

func handleMetrics(c *fiber.Ctx) error {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	data, _ := (MetricsResponse{
		MemMB:   float64(ms.Alloc) / 1048576,
		Threads: runtime.NumGoroutine(),
	}).MarshalJSON()
	return sendJSON(c, data)
}

func fib(n int) int64 {
	if n <= 1 {
		return int64(n)
	}
	return fib(n-1) + fib(n-2)
}
