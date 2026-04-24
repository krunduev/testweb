package com.bench;

import com.fasterxml.jackson.databind.JsonNode;
import org.springframework.lang.Nullable;
import org.springframework.web.bind.annotation.*;

import java.lang.management.ManagementFactory;
import java.util.Map;

@RestController
public class BenchController {

    @GetMapping("/ping")
    public Map<String, String> ping() {
        return Map.of("status", "ok", "service", "java-spring");
    }

    @GetMapping("/fibonacci")
    public Map<String, Object> fibonacci(@RequestParam(defaultValue = "35") int n) {
        long start = System.nanoTime();
        long result = fib(n);
        double timeMs = (System.nanoTime() - start) / 1_000_000.0;
        return Map.of("n", n, "result", result, "time_ms", timeMs);
    }

    @PostMapping("/echo")
    public JsonNode echo(@RequestBody JsonNode body) {
        return body;
    }

    @GetMapping("/alloc")
    public Map<String, Object> alloc(@RequestParam(defaultValue = "1048576") int size) {
        byte[] buf = new byte[size];
        buf[0] = 0;
        return Map.of(
            "requested_bytes", size,
            "alloced_mb", size / 1_048_576.0
        );
    }

    @GetMapping("/metrics")
    public Map<String, Object> metrics() {
        Runtime rt = Runtime.getRuntime();
        double memMB = (rt.totalMemory() - rt.freeMemory()) / 1_048_576.0;
        int threads = ManagementFactory.getThreadMXBean().getThreadCount();
        return Map.of("mem_mb", memMB, "threads", threads);
    }

    private long fib(int n) {
        if (n <= 1) return n;
        return fib(n - 1) + fib(n - 2);
    }
}
