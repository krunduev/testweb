using System.Diagnostics;
using System.Text.Json;

var builder = WebApplication.CreateBuilder(args);
builder.WebHost.UseUrls("http://0.0.0.0:8085");
builder.Services.ConfigureHttpJsonOptions(opts =>
{
    opts.SerializerOptions.PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower;
});

var app = builder.Build();

app.MapGet("/ping", () =>
    new { status = "ok", service = "dotnet" });

app.MapGet("/fibonacci", (int n = 35) =>
{
    var sw = Stopwatch.StartNew();
    long result = Fib(n);
    sw.Stop();
    return new { n, result, time_ms = sw.Elapsed.TotalMilliseconds };
});

app.MapPost("/echo", async (HttpContext ctx) =>
{
    ctx.Response.ContentType = "application/json";
    await ctx.Request.Body.CopyToAsync(ctx.Response.Body);
});

app.MapGet("/alloc", (int size = 1048576) =>
{
    byte[] buf = new byte[size];
    buf[0] = 0;
    return new { requested_bytes = size, alloced_mb = size / 1_048_576.0 };
});

app.MapGet("/metrics", () =>
{
    var process = Process.GetCurrentProcess();
    double memMb = GC.GetTotalMemory(false) / 1_048_576.0;
    int threads = process.Threads.Count;
    return new { mem_mb = memMb, threads };
});

app.Run();

static long Fib(int n)
{
    if (n <= 1) return n;
    return Fib(n - 1) + Fib(n - 2);
}
