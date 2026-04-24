package main

import (
	"runtime"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

const serviceName = "go-fiber"

func main() {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/ping", handlePing)
	app.Get("/fibonacci", handleFibonacci)
	app.Post("/echo", handleEcho)
	app.Get("/alloc", handleAlloc)
	app.Get("/metrics", handleMetrics)
	app.Listen(":8088")
}

func handlePing(c *fiber.Ctx) error {
	return c.JSON(PingResponse{Status: "ok", Service: serviceName})
}

func handleFibonacci(c *fiber.Ctx) error {
	n, _ := strconv.Atoi(c.Query("n"))
	if n <= 0 {
		n = 35
	}
	start := time.Now()
	result := fib(n)
	elapsed := time.Since(start).Seconds() * 1000
	return c.JSON(FibResponse{N: n, Result: result, TimeMs: elapsed})
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
	return c.JSON(AllocResponse{
		RequestedBytes: size,
		AllocedMB:      float64(size) / 1048576.0,
	})
}

func handleMetrics(c *fiber.Ctx) error {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return c.JSON(MetricsResponse{
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
