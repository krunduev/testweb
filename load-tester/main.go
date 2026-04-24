package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// ─── Config ──────────────────────────────────────────────────────────────────

type ServiceCfg struct {
	Name     string `yaml:"name"      json:"name"`
	URL      string `yaml:"url"       json:"url"`
	StartDir string `yaml:"start_dir" json:"-"`
	StartCmd string `yaml:"start_cmd" json:"-"`
}

type Config struct {
	Autostart      bool         `yaml:"autostart"`
	StartTimeout   string       `yaml:"start_timeout"`
	StageDuration  string       `yaml:"stage_duration"`
	MaxWorkers     int          `yaml:"max_workers"`
	Stages         []int        `yaml:"stages"`
	FibN           int          `yaml:"fib_n"`
	AllocSize      int          `yaml:"alloc_size"`
	ErrThreshold   float64      `yaml:"err_threshold"`
	P99Threshold   float64      `yaml:"p99_threshold"`
	OutputHTML     string       `yaml:"output_html"`
	Cooldown       string       `yaml:"cooldown"`
	RequestTimeout string       `yaml:"request_timeout"`
	Services       []ServiceCfg `yaml:"services"`
}

var builtinConfig = Config{
	StageDuration:  "30s",
	MaxWorkers:     2000,
	Stages:         []int{10, 25, 50, 100, 200, 400, 800, 1200, 1600, 2000},
	FibN:           35,
	AllocSize:      1048576,
	ErrThreshold:   5.0,
	P99Threshold:   5000.0,
	OutputHTML:     "report.html",
	Cooldown:       "2s",
	RequestTimeout: "5s",
	Services: []ServiceCfg{
		{Name: "stdlib-ej", URL: "http://localhost:8081"},
		{Name: "gin-ej",    URL: "http://localhost:8082"},
		{Name: "fiber-ej",  URL: "http://localhost:8083"},
		{Name: "spring",    URL: "http://localhost:8084"},
		{Name: "dotnet",    URL: "http://localhost:8085"},
		{Name: "stdlib",    URL: "http://localhost:8086"},
		{Name: "gin",       URL: "http://localhost:8087"},
		{Name: "fiber",     URL: "http://localhost:8088"},
	},
}

// ─── Result types ────────────────────────────────────────────────────────────

type Endpoint struct {
	Name   string
	Method string
	Path   string
	Body   []byte
}

type StageResult struct {
	Workers  int     `json:"workers"`
	RPS      float64 `json:"rps"`
	P50      float64 `json:"p50"`
	P95      float64 `json:"p95"`
	P99      float64 `json:"p99"`
	MaxMs    float64 `json:"max_ms"`
	ErrPct   float64 `json:"err_pct"`
	MemMB    float64 `json:"mem_mb"`
	Threads  int     `json:"threads"`
	HitLimit bool    `json:"hit_limit"`
}

type ServiceResult struct {
	Service ServiceCfg    `json:"service"`
	Stages  []StageResult `json:"stages"`
	PeakRPS float64       `json:"peak_rps"`
}

type EndpointResult struct {
	Name     string          `json:"endpoint"`
	ChartID  string          `json:"chart_id"`
	Services []ServiceResult `json:"services"`
	ep       Endpoint
}

type metricsResp struct {
	MemMB   float64 `json:"mem_mb"`
	Threads int     `json:"threads"`
}

// ─── Main ────────────────────────────────────────────────────────────────────

func main() {
	configFile  := flag.String("config",     "config.yaml", "config YAML file path")
	maxWorkers  := flag.Int("max-workers",   0,             "override max_workers (0 = use config)")
	outputHTML  := flag.String("output",     "",            "override output HTML path")
	stageDurStr := flag.String("stage-dur",  "",            "override stage_duration, e.g. 10s")
	flag.Parse()

	cfg := loadConfig(*configFile)
	if *maxWorkers > 0    { cfg.MaxWorkers = *maxWorkers }
	if *outputHTML != ""  { cfg.OutputHTML = *outputHTML }
	if *stageDurStr != "" { cfg.StageDuration = *stageDurStr }

	stageDur, _    := time.ParseDuration(cfg.StageDuration)
	cooldownDur, _ := time.ParseDuration(cfg.Cooldown)
	reqTimeout, _  := time.ParseDuration(cfg.RequestTimeout)
	stages := cappedStages(cfg.Stages, cfg.MaxWorkers)

	fmt.Printf("Config: stage=%s  max_workers=%d  stages=%v\n\n", cfg.StageDuration, cfg.MaxWorkers, stages)

	// Auto-start services if configured
	var procs []*exec.Cmd
	if cfg.Autostart {
		startTimeout, _ := time.ParseDuration(cfg.StartTimeout)
		if startTimeout == 0 {
			startTimeout = 60 * time.Second
		}
		configDir := filepath.Dir(*configFile)
		var ready []ServiceCfg
		procs, ready = startServices(cfg, configDir, startTimeout)
		cfg.Services = ready
		defer stopServices(procs)

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sig
			stopServices(procs)
			os.Exit(0)
		}()
	}

	// Health check — ping every service before starting long tests
	if !healthCheck(cfg) {
		fmt.Fprintln(os.Stderr, "\nAbort: fix the services above and retry.")
		stopServices(procs)
		os.Exit(1)
	}

	endpoints := []Endpoint{
		{Name: "GET /ping",   Method: "GET",  Path: "/ping"},
		{
			Name:   fmt.Sprintf("GET /fibonacci?n=%d", cfg.FibN),
			Method: "GET",
			Path:   fmt.Sprintf("/fibonacci?n=%d", cfg.FibN),
		},
		{Name: "POST /echo",  Method: "POST", Path: "/echo", Body: []byte(`{"hello":"world","bench":true}`)},
		{
			Name:   fmt.Sprintf("GET /alloc?size=%d", cfg.AllocSize),
			Method: "GET",
			Path:   fmt.Sprintf("/alloc?size=%d", cfg.AllocSize),
		},
	}

	var allResults []EndpointResult

	for _, ep := range endpoints {
		bar := strings.Repeat("═", 80)
		fmt.Printf("\n%s\n  %s\n%s\n", bar, ep.Name, bar)

		epRes := EndpointResult{Name: ep.Name, ChartID: toChartID(ep.Name), ep: ep}

		for _, svc := range cfg.Services {
			fmt.Printf("\n  ▶ %s  (%s)\n", svc.Name, svc.URL)
			printHeader()

			svcRes := ServiceResult{Service: svc}
			svcRes.Stages = runRamp(svc.URL, ep, stages, stageDur, cooldownDur, reqTimeout, cfg, func(r StageResult) {
				if r.RPS > svcRes.PeakRPS {
					svcRes.PeakRPS = r.RPS
				}
				printRow(r)
			})
			printFooter()
			epRes.Services = append(epRes.Services, svcRes)

			// longer pause between services so the tested machine can cool down
			time.Sleep(cooldownDur * 3)
		}

		allResults = append(allResults, epRes)
	}

	printSummary(allResults)

	if err := generateReport(cfg, allResults, stages); err != nil {
		fmt.Fprintln(os.Stderr, "HTML report error:", err)
	} else {
		fmt.Printf("\nHTML report saved → %s\n", cfg.OutputHTML)
	}
}

func loadConfig(path string) Config {
	cfg := builtinConfig
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("No %s found, using built-in defaults\n\n", path)
		return cfg
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "config parse error: %v — using built-in defaults\n", err)
	} else {
		fmt.Printf("Loaded config from %s\n", path)
	}
	return cfg
}

func cappedStages(stages []int, max int) []int {
	var out []int
	for _, s := range stages {
		if s <= max {
			out = append(out, s)
		}
	}
	if len(out) == 0 || out[len(out)-1] < max {
		out = append(out, max)
	}
	return out
}

// ─── Ramp test ───────────────────────────────────────────────────────────────

func runRamp(baseURL string, ep Endpoint, stages []int, stageDur, cooldown, reqTimeout time.Duration, cfg Config, onStage func(StageResult)) []StageResult {
	maxConn := stages[len(stages)-1] + 200
	client := &http.Client{
		Timeout: reqTimeout,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: maxConn,
			MaxConnsPerHost:     maxConn,
		},
	}

	var results []StageResult
	url := baseURL + ep.Path

	for _, workers := range stages {
		fmt.Printf("    %-8d  running...%-68s\r", workers, "")
		mem := queryMetrics(client, baseURL)
		r := runStage(client, url, ep.Method, ep.Body, workers, stageDur)
		r.MemMB = mem.MemMB
		r.Threads = mem.Threads

		hitLimit := r.ErrPct > cfg.ErrThreshold || r.P99 > cfg.P99Threshold
		if hitLimit {
			r.HitLimit = true
		}
		results = append(results, r)
		onStage(r)

		if hitLimit {
			break
		}
		time.Sleep(cooldown)
	}
	return results
}

const maxLatencySamples = 500_000

func runStage(client *http.Client, url, method string, body []byte, workers int, dur time.Duration) StageResult {
	var totalReqs, totalErrs int64
	latencies := make([]float64, 0, min(workers*200, maxLatencySamples))
	var mu sync.Mutex
	stop := make(chan struct{})
	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}

				var bodyR io.Reader
				if len(body) > 0 {
					bodyR = bytes.NewReader(body)
				}
				req, _ := http.NewRequest(method, url, bodyR)
				if len(body) > 0 {
					req.Header.Set("Content-Type", "application/json")
				}

				t0 := time.Now()
				resp, err := client.Do(req)
				ms := float64(time.Since(t0).Microseconds()) / 1000.0

				atomic.AddInt64(&totalReqs, 1)
				if err != nil || resp == nil || resp.StatusCode >= 500 {
					atomic.AddInt64(&totalErrs, 1)
				} else {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}

				mu.Lock()
				if len(latencies) < maxLatencySamples {
					latencies = append(latencies, ms)
				}
				mu.Unlock()
			}
		}()
	}

	time.Sleep(dur)
	close(stop)
	wg.Wait()

	elapsed := time.Since(start).Seconds()
	reqs := atomic.LoadInt64(&totalReqs)
	errs := atomic.LoadInt64(&totalErrs)
	sort.Float64s(latencies)

	errPct := 0.0
	if reqs > 0 {
		errPct = float64(errs) / float64(reqs) * 100
	}
	return StageResult{
		Workers: workers,
		RPS:     float64(reqs) / elapsed,
		P50:     pct(latencies, 50),
		P95:     pct(latencies, 95),
		P99:     pct(latencies, 99),
		MaxMs:   sliceMax(latencies),
		ErrPct:  errPct,
	}
}

func queryMetrics(client *http.Client, baseURL string) metricsResp {
	resp, err := client.Get(baseURL + "/metrics")
	if err != nil {
		return metricsResp{}
	}
	defer resp.Body.Close()
	var m metricsResp
	json.NewDecoder(resp.Body).Decode(&m)
	return m
}

// ─── Math helpers ────────────────────────────────────────────────────────────

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func sliceMax(s []float64) float64 {
	m := 0.0
	for _, v := range s {
		if v > m {
			m = v
		}
	}
	return m
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─── Console output ──────────────────────────────────────────────────────────

func printHeader() {
	fmt.Printf("    %-8s  %-10s  %-8s  %-8s  %-8s  %-8s  %-6s  %-8s  %s\n",
		"Workers", "RPS", "P50ms", "P95ms", "P99ms", "Maxms", "Err%", "Mem MB", "Threads")
	fmt.Println("    " + strings.Repeat("─", 78))
}

func printFooter() {
	fmt.Println("    " + strings.Repeat("─", 78))
}

func printRow(r StageResult) {
	limit := ""
	if r.HitLimit {
		limit = "  ← LIMIT"
	}
	fmt.Printf("    %-8d  %-10.0f  %-8.2f  %-8.2f  %-8.2f  %-8.2f  %-6.2f  %-8.1f  %-7d%s\n",
		r.Workers, r.RPS, r.P50, r.P95, r.P99, r.MaxMs, r.ErrPct, r.MemMB, r.Threads, limit)
}

func printSummary(results []EndpointResult) {
	bar := strings.Repeat("═", 80)
	fmt.Printf("\n%s\n  PEAK RPS SUMMARY\n%s\n", bar, bar)
	for _, ep := range results {
		fmt.Printf("\n  %s\n", ep.Name)
		sorted := make([]ServiceResult, len(ep.Services))
		copy(sorted, ep.Services)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].PeakRPS > sorted[j].PeakRPS })
		for i, svc := range sorted {
			bar := "░"
			w := int(svc.PeakRPS / sorted[0].PeakRPS * 30)
			if w > 0 {
				bar = strings.Repeat("█", w)
			}
			fmt.Printf("    %d. %-14s %s %.0f RPS\n", i+1, svc.Service.Name, bar, svc.PeakRPS)
		}
	}
}

// ─── Autostart ───────────────────────────────────────────────────────────────

// startServices launches all configured services, redirects their output to
// log files, and returns (processes, services-that-actually-started).
// Services whose binary is not found are silently skipped so the benchmark
// can continue with the remaining ones.
func startServices(cfg Config, configDir string, timeout time.Duration) ([]*exec.Cmd, []ServiceCfg) {
	var procs []*exec.Cmd
	var started []ServiceCfg

	for _, svc := range cfg.Services {
		if svc.StartCmd == "" {
			started = append(started, svc) // manually managed, assume it's up
			continue
		}

		dir := svc.StartDir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(configDir, dir)
		}

		parts := strings.Fields(svc.StartCmd)
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Dir = dir
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard

		if err := cmd.Start(); err != nil {
			fmt.Printf("  [autostart] ✗  %-12s FAILED TO START — %v\n", svc.Name, err)
			// keep in started so healthCheck reports it clearly
			started = append(started, svc)
			continue
		}
		procs = append(procs, cmd)
		started = append(started, svc)
		fmt.Printf("  [autostart] ✓  %-12s pid %d\n", svc.Name, cmd.Process.Pid)
	}

	// Each service gets its own independent timeout starting from NOW.
	client := &http.Client{Timeout: 2 * time.Second}
	// Wait for each service to respond on /ping (independent timeout per service).
	for _, svc := range started {
		if svc.StartCmd == "" {
			continue
		}
		fmt.Printf("  [autostart] waiting for %s ...", svc.Name)
		ok := false
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			resp, err := client.Get(svc.URL + "/ping")
			if err == nil && resp.StatusCode == 200 {
				resp.Body.Close()
				ok = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if ok {
			fmt.Println(" ready")
		} else {
			fmt.Println(" TIMEOUT")
		}
	}

	fmt.Println()
	return procs, started
}

var installHints = map[string]string{
	"spring": "Maven not installed. Fix: sudo apt install maven  OR  brew install maven",
	"dotnet": ".NET SDK missing. Fix: https://aka.ms/dotnet-install",
}

func healthCheck(cfg Config) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	bar := strings.Repeat("─", 56)
	fmt.Printf("\n%s\n  Health check\n%s\n", bar, bar)

	allOK := true
	for _, svc := range cfg.Services {
		resp, err := client.Get(svc.URL + "/ping")
		if err != nil || resp.StatusCode != 200 {
			fmt.Printf("  ✗  %-12s %s  — DOWN\n", svc.Name, svc.URL)
			if hint, ok := installHints[svc.Name]; ok {
				fmt.Printf("     hint: %s\n", hint)
			}
			allOK = false
		} else {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			fmt.Printf("  ✓  %-12s %s  — %s\n", svc.Name, svc.URL, strings.TrimSpace(string(body)))
		}
	}
	fmt.Println(bar)
	if allOK {
		fmt.Println("  All services are up. Starting benchmark...\n")
	}
	return allOK
}

func stopServices(procs []*exec.Cmd) {
	for _, cmd := range procs {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func toChartID(name string) string {
	r := strings.NewReplacer(" ", "-", "/", "", "?", "", "=", "", "&", "", ".", "")
	return "ch-" + strings.ToLower(r.Replace(name))
}
