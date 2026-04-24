package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"time"
)

type reportData struct {
	GeneratedAt string
	Config      Config
	Results     []EndpointResult
	Stages      []int
	JSONData    template.JS
}

func generateReport(cfg Config, results []EndpointResult, stages []int) error {
	type jsPayload struct {
		Results []EndpointResult `json:"results"`
	}
	raw, err := json.Marshal(jsPayload{Results: results})
	if err != nil {
		return err
	}

	funcMap := template.FuncMap{
		"formatRPS": func(f float64) string { return fmt.Sprintf("%.0f", f) },
		"formatMs":  func(f float64) string { return fmt.Sprintf("%.2f", f) },
		"formatPct": func(f float64) string { return fmt.Sprintf("%.2f%%", f) },
		"formatF1":  func(f float64) string { return fmt.Sprintf("%.1f", f) },
		"formatBytes": func(n int) string {
			switch {
			case n >= 1_048_576:
				return fmt.Sprintf("%.0f MB", float64(n)/1_048_576)
			case n >= 1024:
				return fmt.Sprintf("%.0f KB", float64(n)/1024)
			default:
				return fmt.Sprintf("%d B", n)
			}
		},
		"errClass": func(pct float64) string {
			switch {
			case pct == 0:
				return "ok"
			case pct < 5.0:
				return "warn"
			default:
				return "bad"
			}
		},
	}

	tmpl, err := template.New("report").Funcs(funcMap).Parse(htmlTmpl)
	if err != nil {
		return err
	}

	f, err := os.Create(cfg.OutputHTML)
	if err != nil {
		return err
	}
	defer f.Close()

	return tmpl.Execute(f, reportData{
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		Config:      cfg,
		Results:     results,
		Stages:      stages,
		JSONData:    template.JS(raw),
	})
}

const htmlTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Benchmark Report</title>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.0/dist/chart.umd.min.js"></script>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#0a0f1e;color:#e2e8f0;padding:32px 40px;font-size:14px;line-height:1.5}
a{color:#38bdf8}
h1{font-size:30px;font-weight:800;color:#f8fafc;margin-bottom:6px}
h2{font-size:18px;font-weight:700;color:#7dd3fc;margin:48px 0 16px;border-bottom:1px solid #1e293b;padding-bottom:10px;letter-spacing:.02em}
h3{font-size:14px;font-weight:700;color:#f1f5f9;margin-bottom:0}
.meta{color:#475569;font-size:13px;margin-bottom:28px}

.cfg-grid{display:flex;flex-wrap:wrap;gap:12px;margin-bottom:40px}
.cfg-card{background:#111827;border:1px solid #1e293b;border-radius:10px;padding:14px 20px;min-width:130px}
.cfg-label{font-size:10px;color:#475569;text-transform:uppercase;letter-spacing:.08em;margin-bottom:4px}
.cfg-val{font-size:22px;font-weight:800;color:#f1f5f9}

.chart-box{background:#111827;border:1px solid #1e293b;border-radius:14px;padding:20px;margin-bottom:24px;height:380px;position:relative}

.svc-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(560px,1fr));gap:14px;margin-bottom:36px}
.svc-card{background:#111827;border:1px solid #1e293b;border-radius:12px;padding:18px 20px}
.svc-head{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:14px}
.svc-name{font-size:15px;font-weight:700;color:#f1f5f9}
.svc-url{font-size:11px;color:#475569;margin-top:2px}
.peak{font-size:22px;font-weight:800;color:#22d3ee;white-space:nowrap;line-height:1}
.peak-label{font-size:10px;color:#475569;text-align:right;margin-top:2px}

table{width:100%;border-collapse:collapse;font-size:12px}
th{background:#0a0f1e;color:#64748b;text-align:right;padding:6px 10px;font-size:11px;font-weight:600;letter-spacing:.05em;white-space:nowrap;border-bottom:1px solid #1e293b}
th:first-child{text-align:left}
td{padding:5px 10px;text-align:right;border-bottom:1px solid #0f172a;color:#94a3b8;font-variant-numeric:tabular-nums}
td:first-child{text-align:left;color:#cbd5e1;font-weight:600}
tr:last-child td{border-bottom:none}
tr.lim{background:#1c0a0a}
tr.lim td{color:#f87171}

.ok{color:#4ade80;font-weight:600}
.warn{color:#fbbf24}
.bad{color:#f87171;font-weight:700}
.pill{display:inline-block;background:#7f1d1d;color:#fca5a5;border-radius:4px;padding:0 5px;font-size:10px;margin-left:6px;vertical-align:middle}
</style>
</head>
<body>

<h1>Web Service Benchmark Report</h1>
<p class="meta">Generated {{ .GeneratedAt }} &nbsp;&bull;&nbsp; Stage: {{ .Config.StageDuration }} &nbsp;&bull;&nbsp; Max workers: {{ .Config.MaxWorkers }}</p>

<div class="cfg-grid">
  <div class="cfg-card"><div class="cfg-label">Max Workers</div><div class="cfg-val">{{ .Config.MaxWorkers }}</div></div>
  <div class="cfg-card"><div class="cfg-label">Stage Duration</div><div class="cfg-val">{{ .Config.StageDuration }}</div></div>
  <div class="cfg-card"><div class="cfg-label">Fibonacci N</div><div class="cfg-val">{{ .Config.FibN }}</div></div>
  <div class="cfg-card"><div class="cfg-label">Alloc Size</div><div class="cfg-val">{{ formatBytes .Config.AllocSize }}</div></div>
  <div class="cfg-card"><div class="cfg-label">Err Threshold</div><div class="cfg-val">{{ .Config.ErrThreshold }}%</div></div>
  <div class="cfg-card"><div class="cfg-label">P99 Limit</div><div class="cfg-val">{{ .Config.P99Threshold }}ms</div></div>
  <div class="cfg-card"><div class="cfg-label">Services</div><div class="cfg-val">{{ len .Config.Services }}</div></div>
</div>

<h2>Peak RPS — All Endpoints</h2>
<div class="chart-box"><canvas id="chart-summary"></canvas></div>

{{ range .Results }}
<h2>{{ .Name }}</h2>
<div class="chart-box"><canvas id="{{ .ChartID }}"></canvas></div>

<div class="svc-grid">{{ range .Services }}
<div class="svc-card">
  <div class="svc-head">
    <div>
      <div class="svc-name">{{ .Service.Name }}</div>
      <div class="svc-url">{{ .Service.URL }}</div>
    </div>
    <div>
      <div class="peak">{{ formatRPS .PeakRPS }}</div>
      <div class="peak-label">peak RPS</div>
    </div>
  </div>
  <table>
    <thead><tr>
      <th>Workers</th><th>RPS</th><th>P50ms</th><th>P95ms</th><th>P99ms</th>
      <th>Max ms</th><th>Err%</th><th>Mem MB</th><th>Threads</th>
    </tr></thead>
    <tbody>{{ range .Stages }}
      <tr{{ if .HitLimit }} class="lim"{{ end }}>
        <td>{{ .Workers }}{{ if .HitLimit }}<span class="pill">LIMIT</span>{{ end }}</td>
        <td>{{ formatRPS .RPS }}</td>
        <td>{{ formatMs .P50 }}</td>
        <td>{{ formatMs .P95 }}</td>
        <td>{{ formatMs .P99 }}</td>
        <td>{{ formatMs .MaxMs }}</td>
        <td class="{{ errClass .ErrPct }}">{{ formatPct .ErrPct }}</td>
        <td>{{ formatF1 .MemMB }}</td>
        <td>{{ .Threads }}</td>
      </tr>{{ end }}
    </tbody>
  </table>
</div>{{ end }}
</div>
{{ end }}

<script>
var DATA = {{ .JSONData }};

var COLORS = [
  "#38bdf8","#4ade80","#fb923c","#f87171",
  "#a78bfa","#34d399","#fbbf24","#818cf8"
];
var GRID   = "#1e293b";
var LABEL  = "#64748b";
var TICK   = "#94a3b8";

Chart.defaults.color = TICK;
Chart.defaults.borderColor = GRID;

function axes(xTitle, yTitle) {
  return {
    x: { title:{display:true,text:xTitle,color:LABEL}, ticks:{color:TICK}, grid:{color:GRID} },
    y: { title:{display:true,text:yTitle,color:LABEL}, beginAtZero:true, ticks:{color:TICK}, grid:{color:GRID} }
  };
}

function legend() {
  return { position:"top", labels:{ color:TICK, padding:16, boxWidth:12 } };
}

/* Per-endpoint RPS line charts */
DATA.results.forEach(function(ep) {
  var workerSet = {};
  ep.services.forEach(function(s) {
    s.stages.forEach(function(st) { workerSet[st.workers] = true; });
  });
  var workers = Object.keys(workerSet).map(Number).sort(function(a,b){return a-b;});

  var datasets = ep.services.map(function(svc, i) {
    return {
      label: svc.service.name,
      data: workers.map(function(w) {
        var s = svc.stages.find(function(st){ return st.workers === w; });
        return s ? Math.round(s.rps) : null;
      }),
      borderColor: COLORS[i % COLORS.length],
      backgroundColor: COLORS[i % COLORS.length] + "28",
      fill: false, tension: 0.35, spanGaps: true,
      pointRadius: 4, pointHoverRadius: 7,
      borderWidth: 2
    };
  });

  new Chart(document.getElementById(ep.chart_id), {
    type: "line",
    data: { labels: workers.map(String), datasets: datasets },
    options: {
      responsive: true, maintainAspectRatio: false,
      plugins: { legend: legend() },
      scales: axes("Workers", "RPS")
    }
  });
});

/* Summary grouped bar chart */
var epLabels = DATA.results.map(function(e) { return e.endpoint; });
var svcNames = DATA.results[0].services.map(function(s) { return s.service.name; });

var summaryDS = svcNames.map(function(name, i) {
  return {
    label: name,
    data: DATA.results.map(function(ep) {
      var s = ep.services.find(function(sv){ return sv.service.name === name; });
      return s ? Math.round(s.peak_rps) : 0;
    }),
    backgroundColor: COLORS[i % COLORS.length] + "cc",
    borderColor:     COLORS[i % COLORS.length],
    borderWidth: 1
  };
});

new Chart(document.getElementById("chart-summary"), {
  type: "bar",
  data: { labels: epLabels, datasets: summaryDS },
  options: {
    responsive: true, maintainAspectRatio: false,
    plugins: { legend: legend() },
    scales: axes("Endpoint", "Peak RPS")
  }
});
</script>
</body>
</html>`
