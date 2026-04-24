# Продвинутая лекция: разработка веб-сервисов на Go в 2026

> Файл предназначен для сохранения как `/mnt/user-data/outputs/go-web-services-advanced.md`.
> Целевая аудитория: инженеры, уверенно владеющие Go (интерфейсы, каналы, `io.Reader`), изучающие серверную разработку на продвинутом уровне.
> Актуально на апрель 2026: Go 1.26.2, Gin v1.12.0, Fiber v3.1.0.

---

## 1. Введение: экосистема HTTP в Go

Go изначально спроектирован как язык серверов. Пакет `net/http` — это не «стандартный минимум», как в C или Python, а полноценный production-grade HTTP/1.1 и HTTP/2 стек с роутером, клиентом, поддержкой TLS, keep-alive, HTTP/2 server push и graceful shutdown. На `net/http` построены Kubernetes, Prometheus, Docker, CockroachDB, VictoriaMetrics. Поэтому первый и главный вопрос звучит не «какой фреймворк взять», а **«а нужен ли мне фреймворк вообще»**.

До Go 1.22 ответ часто был «да», потому что стандартный `http.ServeMux` умел матчить только префиксы путей: никаких методов, никаких path-параметров, никаких wildcard. Всё, что сложнее `/users/` приходилось писать руками или тянуть chi/gorilla/mux. **В феврале 2024 Go 1.22 выпустил новый `ServeMux` с pattern matching** — методы, параметры пути, wildcard-«хвосты», приоритизация маршрутов. Это событие перекроило ландшафт: для 80% API stdlib теперь достаточно.

**Зачем тогда фреймворки.** Три причины: (1) эргономика — `c.JSON(200, obj)` короче, чем ручная сериализация и запись; (2) готовые middleware — CORS, JWT, rate-limit, Prometheus, OpenTelemetry из коробки; (3) производительность в узкой нише — Fiber на базе `fasthttp` даёт до 3–5× RPS на коротких JSON-ответах. Но за каждую из этих причин платят архитектурной ценой: совместимостью с net/http-экосистемой, поддержкой HTTP/2, интеграцией с `context.Context`, возможностью использовать `httptest`.

Эта лекция строится вокруг **одного сквозного примера** — TODO API с шестью эндпоинтами (`GET/POST/PUT/DELETE /tasks`, `GET /tasks/{id}`, `GET /health`), реализованного на трёх стеках: чистом `net/http` из Go 1.22+, на Gin и на Fiber v3. Цель — не «показать как работает», а дать инструмент осознанного выбора.

---

## 2. Архитектура: net/http против fasthttp

Ключевая развилка всей экосистемы Go-вебфреймворков проходит не между Gin и Fiber, а между **`net/http` и `fasthttp`**. Это два несовместимых транспорта с разной моделью памяти. Gin, Echo, chi, Huma, `ServeMux` — все используют `net/http`. Fiber — единственный из широко известных, кто построен на `fasthttp`. Разобравшись в этой разнице, всё остальное становится техникой.

### Модель net/http

Стандартная библиотека следует простой и дорогой модели: **goroutine per connection**. На каждое входящее TCP-соединение сервер поднимает горутину. На каждый HTTP-запрос внутри соединения создаёт **новые** `*http.Request` и `http.ResponseWriter`. Заголовки хранятся в `map[string][]string`, тело — в `io.ReadCloser`, путь и параметры — в `url.URL` (строки). Всё это типобезопасно, иммутабельно с точки зрения потребителя и дружит с `context.Context`: у `http.Request` есть метод `Context()`, и его `context.Context` прозрачно пробрасывается в базу данных, HTTP-клиенты, `slog.InfoContext`.

Цена — аллокации. Для каждого запроса: `Request`, `URL`, `Header` map, `Values`, обёртки вокруг `net.Conn`. GC выдерживает, но под нагрузкой 100k+ RPS мусор становится заметен в p99 latency.

**Плюсы net/http:** HTTP/2 из коробки (через TLS или h2c), интеграция со всей экосистемой (`httptest`, `otelhttp`, chi middleware, любой `func(http.Handler) http.Handler`), стабильный API с обратной совместимостью, полноценный `context.Context` по всей цепочке.

### Модель fasthttp

`fasthttp` (валерия Копытова, с 2015 года) написан с нуля ради одного: **минимум аллокаций на один запрос**. Принципы: один объект `*fasthttp.RequestCtx` на запрос, возвращается в `sync.Pool` после обработки; заголовки и тело — `[]byte`-слайсы, а не строки; нет пары `(ResponseWriter, *Request)` — всё в одном контексте; TCP-соединение обслуживается в пуле рабочих горутин, а не «одна горутина на соединение».

**Последствия этой модели — самая важная часть лекции.** Во-первых, `RequestCtx` и его поля после возврата из хендлера **инвалидируются**. Если middleware захочет сохранить ссылку на тело запроса в фоновой горутине — получит гонку данных или мусор. Приходится копировать вручную. Во-вторых, **fasthttp не поддерживает HTTP/2 и HTTP/3**. Это не временное ограничение: архитектурно fasthttp оптимизирован под модель «одно соединение — последовательные запросы», а HTTP/2 — это мультиплексирование множества потоков внутри одного соединения. Issue о поддержке HTTP/2 открыт с 2016 года и помечен «under construction». В-третьих, **ни один стандартный `func(http.Handler) http.Handler` middleware не работает напрямую** — нужны адаптеры, которые теряют zero-copy-преимущества.

Автор fasthttp сам пишет в README: *«Unless your server needs to handle thousands of small to medium requests per second and needs a consistent low millisecond response time fasthttp might not be for you. For most cases net/http is much better.»*

### Почему это критично для production

Представьте три реальных требования: (1) gRPC-Gateway, который конвертирует HTTP/JSON в gRPC, — **требует net/http**; (2) OpenTelemetry трассировка через `otelhttp.NewHandler()` — **требует net/http-handler**; (3) HTTP/2-клиент от мобильного приложения с мультиплексированием — **требует HTTP/2**. В каждом из этих сценариев Fiber исключён не потому, что медленный, а потому что архитектурно несовместим.

Fiber v3 (февраль 2026) частично смягчил ситуацию: его роутер умеет принимать **семнадцать разных сигнатур хендлеров**, включая `http.HandlerFunc`, `fasthttp.RequestHandler` и Express-style. Есть двунаправленный мост `middleware/adaptor`. Но транспорт остался fasthttp — HTTP/2 всё равно нет.

---

## 3. Реализация на чистом net/http (Go 1.22+)

Все три примера используют одну и ту же модель домена и репозиторий в памяти. Для краткости приведу модель один раз.

```go
// file: internal/domain/task.go
package domain

import (
    "errors"
    "sync"
    "time"
)

type Task struct {
    ID        int64     `json:"id"`
    Title     string    `json:"title"      validate:"required,min=1,max=200"`
    Done      bool      `json:"done"`
    CreatedAt time.Time `json:"created_at"`
}

var ErrNotFound = errors.New("task not found")

type Repo struct {
    mu    sync.RWMutex
    data  map[int64]Task
    seq   int64
}

func NewRepo() *Repo { return &Repo{data: map[int64]Task{}} }

func (r *Repo) List(offset, limit int) []Task {
    r.mu.RLock(); defer r.mu.RUnlock()
    out := make([]Task, 0, limit)
    i := 0
    for _, t := range r.data {
        if i >= offset && len(out) < limit { out = append(out, t) }
        i++
    }
    return out
}
func (r *Repo) Get(id int64) (Task, error) {
    r.mu.RLock(); defer r.mu.RUnlock()
    t, ok := r.data[id]
    if !ok { return Task{}, ErrNotFound }
    return t, nil
}
func (r *Repo) Create(t Task) Task {
    r.mu.Lock(); defer r.mu.Unlock()
    r.seq++
    t.ID = r.seq; t.CreatedAt = time.Now().UTC()
    r.data[t.ID] = t
    return t
}
func (r *Repo) Update(id int64, t Task) (Task, error) {
    r.mu.Lock(); defer r.mu.Unlock()
    old, ok := r.data[id]
    if !ok { return Task{}, ErrNotFound }
    t.ID = id; t.CreatedAt = old.CreatedAt
    r.data[id] = t
    return t, nil
}
func (r *Repo) Delete(id int64) error {
    r.mu.Lock(); defer r.mu.Unlock()
    if _, ok := r.data[id]; !ok { return ErrNotFound }
    delete(r.data, id); return nil
}
```

### main.go на stdlib

```go
package main

import (
    "context"
    "encoding/json"
    "errors"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "strconv"
    "syscall"
    "time"

    "github.com/go-playground/validator/v10"
    "example.com/todo/internal/domain"
)

type ctxKey int
const loggerKey ctxKey = 1

type App struct {
    repo   *domain.Repo
    log    *slog.Logger
    valid  *validator.Validate
}

// --- middleware ---

func (a *App) withLogger(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        rw := &statusWriter{ResponseWriter: w, status: 200}
        ctx := context.WithValue(r.Context(), loggerKey, a.log.With(
            "method", r.Method, "path", r.URL.Path,
        ))
        next.ServeHTTP(rw, r.WithContext(ctx))
        a.log.LogAttrs(r.Context(), slog.LevelInfo, "http",
            slog.Int("status", rw.status),
            slog.String("method", r.Method),
            slog.String("path", r.URL.Path),
            slog.Duration("dur", time.Since(start)),
        )
    })
}

func recoverer(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                slog.Error("panic", "err", rec)
                http.Error(w, `{"error":"internal"}`, 500)
            }
        }()
        next.ServeHTTP(w, r)
    })
}

type statusWriter struct {
    http.ResponseWriter
    status int
}
func (s *statusWriter) WriteHeader(c int) { s.status = c; s.ResponseWriter.WriteHeader(c) }

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, status int, msg string) {
    writeJSON(w, status, map[string]string{"error": msg})
}

// --- handlers ---

func (a *App) listTasks(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query()
    offset, _ := strconv.Atoi(q.Get("offset"))
    limit, _ := strconv.Atoi(q.Get("limit"))
    if limit <= 0 || limit > 100 { limit = 20 }
    writeJSON(w, 200, a.repo.List(offset, limit))
}

func (a *App) getTask(w http.ResponseWriter, r *http.Request) {
    id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
    if err != nil { writeErr(w, 400, "bad id"); return }
    t, err := a.repo.Get(id)
    if errors.Is(err, domain.ErrNotFound) { writeErr(w, 404, "not found"); return }
    writeJSON(w, 200, t)
}

func (a *App) createTask(w http.ResponseWriter, r *http.Request) {
    var t domain.Task
    r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB guard
    if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
        writeErr(w, 400, "invalid json: "+err.Error()); return
    }
    if err := a.valid.StructCtx(r.Context(), &t); err != nil {
        writeErr(w, 422, err.Error()); return
    }
    writeJSON(w, 201, a.repo.Create(t))
}

func (a *App) updateTask(w http.ResponseWriter, r *http.Request) {
    id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
    if err != nil { writeErr(w, 400, "bad id"); return }
    var t domain.Task
    if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
        writeErr(w, 400, "invalid json"); return
    }
    if err := a.valid.StructCtx(r.Context(), &t); err != nil {
        writeErr(w, 422, err.Error()); return
    }
    upd, err := a.repo.Update(id, t)
    if errors.Is(err, domain.ErrNotFound) { writeErr(w, 404, "not found"); return }
    writeJSON(w, 200, upd)
}

func (a *App) deleteTask(w http.ResponseWriter, r *http.Request) {
    id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
    if err := a.repo.Delete(id); errors.Is(err, domain.ErrNotFound) {
        writeErr(w, 404, "not found"); return
    }
    w.WriteHeader(204)
}

func health(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, 200, map[string]string{"status": "ok"})
}

// --- bootstrap ---

func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
    slog.SetDefault(logger)

    app := &App{
        repo:  domain.NewRepo(),
        log:   logger,
        valid: validator.New(validator.WithRequiredStructEnabled()),
    }

    mux := http.NewServeMux()
    mux.HandleFunc("GET /health",        health)
    mux.HandleFunc("GET /tasks",         app.listTasks)
    mux.HandleFunc("POST /tasks",        app.createTask)
    mux.HandleFunc("GET /tasks/{id}",    app.getTask)
    mux.HandleFunc("PUT /tasks/{id}",    app.updateTask)
    mux.HandleFunc("DELETE /tasks/{id}", app.deleteTask)

    handler := app.withLogger(recoverer(mux))

    srv := &http.Server{
        Addr:              ":8080",
        Handler:           handler,
        ReadHeaderTimeout: 5 * time.Second,
        ReadTimeout:       15 * time.Second,
        WriteTimeout:      30 * time.Second,
        IdleTimeout:       120 * time.Second,
        MaxHeaderBytes:    1 << 20,
    }

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    go func() {
        logger.Info("listening", "addr", srv.Addr)
        if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
            logger.Error("listen", "err", err); stop()
        }
    }()

    <-ctx.Done()
    logger.Info("shutdown signal")
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    if err := srv.Shutdown(shutdownCtx); err != nil {
        logger.Error("forced shutdown", "err", err)
    }
    logger.Info("stopped")
}
```

**Почему именно так.** Роутинг «метод + путь» через паттерны `ServeMux` Go 1.22+ устраняет необходимость в стороннем роутере для CRUD API. Параметр `{id}` извлекается через `r.PathValue("id")` — без импортов. Middleware — обычная композиция `func(http.Handler) http.Handler`, никакой магии. Валидация через `go-playground/validator` используется явно в хендлере, а не магически через теги — это позволяет возвращать тонко настроенные сообщения об ошибках. **Таймауты `ReadHeaderTimeout: 5s` обязательны** — без них сервер уязвим к Slowloris (gosec правило G112). `signal.NotifyContext` — канонический способ собрать graceful shutdown: один контекст, одно ожидание, одно дерево отмены.

---

## 4. Реализация на Gin (v1.12.0)

Gin — самый популярный HTTP-фреймворк в Go-экосистеме: **88 300 звёзд, 183 000 импортёров на pkg.go.dev**. Сборка v1.12.0 (февраль 2026) требует Go 1.25+, добавила BSON в рендер, `GetError/GetErrorSlice/Delete` в `Context`, поддержку `encoding.UnmarshalText` при URI/query-биндинге, и экспериментальный HTTP/3 через `quic-go` (с v1.11.0).

```go
package main

import (
    "context"
    "errors"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "strconv"
    "syscall"
    "time"

    "github.com/gin-gonic/gin"
    sloggin "github.com/samber/slog-gin"
    "example.com/todo/internal/domain"
)

type App struct{ repo *domain.Repo }

func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    slog.SetDefault(logger)

    gin.SetMode(gin.ReleaseMode)
    r := gin.New()
    r.Use(sloggin.NewWithConfig(logger, sloggin.Config{WithRequestID: true}))
    r.Use(gin.Recovery())
    _ = r.SetTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8"})

    app := &App{repo: domain.NewRepo()}

    r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

    v1 := r.Group("/")
    {
        v1.GET("/tasks",         app.list)
        v1.GET("/tasks/:id",     app.get)
        v1.POST("/tasks",        app.create)
        v1.PUT("/tasks/:id",     app.update)
        v1.DELETE("/tasks/:id",  app.del)
    }

    srv := &http.Server{
        Addr: ":8080", Handler: r,
        ReadHeaderTimeout: 5 * time.Second,
        WriteTimeout:      30 * time.Second,
        IdleTimeout:       120 * time.Second,
    }

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    go func() {
        if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
            logger.Error("listen", "err", err); stop()
        }
    }()
    <-ctx.Done()
    sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    _ = srv.Shutdown(sctx)
}

func (a *App) list(c *gin.Context) {
    offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
    limit,  _ := strconv.Atoi(c.DefaultQuery("limit",  "20"))
    c.JSON(200, a.repo.List(offset, limit))
}
func (a *App) get(c *gin.Context) {
    id, err := strconv.ParseInt(c.Param("id"), 10, 64)
    if err != nil { c.AbortWithStatusJSON(400, gin.H{"error": "bad id"}); return }
    t, err := a.repo.Get(id)
    if errors.Is(err, domain.ErrNotFound) { c.AbortWithStatusJSON(404, gin.H{"error": "not found"}); return }
    c.JSON(200, t)
}
func (a *App) create(c *gin.Context) {
    var t domain.Task
    if err := c.ShouldBindJSON(&t); err != nil {
        c.AbortWithStatusJSON(422, gin.H{"error": err.Error()}); return
    }
    slog.InfoContext(c.Request.Context(), "creating", "title", t.Title)
    c.JSON(201, a.repo.Create(t))
}
func (a *App) update(c *gin.Context) {
    id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
    var t domain.Task
    if err := c.ShouldBindJSON(&t); err != nil {
        c.AbortWithStatusJSON(422, gin.H{"error": err.Error()}); return
    }
    upd, err := a.repo.Update(id, t)
    if errors.Is(err, domain.ErrNotFound) { c.AbortWithStatusJSON(404, gin.H{"error": "not found"}); return }
    c.JSON(200, upd)
}
func (a *App) del(c *gin.Context) {
    id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
    if err := a.repo.Delete(id); errors.Is(err, domain.ErrNotFound) {
        c.AbortWithStatusJSON(404, gin.H{"error": "not found"}); return
    }
    c.Status(204)
}
```

**Идиомы Gin, которые стоит запомнить.** Валидация подключается **тегом `binding:"..."`**, а не `validate:"..."` (Gin маппит на тег `binding`, хотя под капотом живёт `go-playground/validator/v10`). Model `domain.Task` с тегом `validate:"required,..."` поэтому в Gin не валидируется автоматически — нужно либо дублировать тег как `binding`, либо вызывать `validator.New().StructCtx()` явно. Это нередкий источник бага «валидация не срабатывает».

**Gin использует обычный `http.Server`** — ровно поэтому у него есть HTTP/2 из коробки (`srv.ListenAndServeTLS()`), работают `httptest`, `otelhttp`, любой middleware `func(http.Handler) http.Handler` через `gin.WrapH`. Это главное преимущество Gin перед Fiber: **вся экосистема `net/http` доступна**. Контекст запроса — `c.Request.Context()` — стандартный `context.Context`, пробрасываемый в БД и логгер.

**`SetTrustedProxies` — обязательный вызов в production.** По умолчанию Gin доверяет всем прокси при определении `ClientIP()`, что в связке с `X-Forwarded-For` открывает спуфинг IP.

Для OpenTelemetry подключается `otelgin.Middleware("service")` из `go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin` — пакет официально поддерживается OTel-сообществом.

---

## 5. Реализация на Fiber v3

Fiber v3.0.0 вышел **2 февраля 2026** (v3.1.0 — 24 февраля), требует Go 1.25+. Ключевые отличия от v2, которые увидит любой мигрирующий: (1) сигнатура хендлера сменилась с `func(c *fiber.Ctx) error` на `func(c fiber.Ctx) error` — `Ctx` стал **интерфейсом**; (2) парсинг объединён под единый `c.Bind()` API: `c.Bind().JSON(&v)`, `c.Bind().Query(&v)`; (3) `fiber.Ctx` **сам реализует `context.Context`** — никакого `c.UserContext()` больше не нужно; (4) валидация прописывается один раз через `fiber.Config.StructValidator` и применяется автоматически при `Bind()`.

```go
package main

import (
    "context"
    "errors"
    "log/slog"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/go-playground/validator/v10"
    "github.com/goccy/go-json"
    "github.com/gofiber/fiber/v3"
    "github.com/gofiber/fiber/v3/middleware/logger"
    "github.com/gofiber/fiber/v3/middleware/recover"
    slogfiber "github.com/samber/slog-fiber"
    "example.com/todo/internal/domain"
)

type sv struct{ v *validator.Validate }
func (s *sv) Validate(out any) error { return s.v.Struct(out) }

type App struct{ repo *domain.Repo }

func main() {
    logr := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    app := fiber.New(fiber.Config{
        AppName:         "todo",
        ReadTimeout:     5 * time.Second,
        WriteTimeout:    30 * time.Second,
        IdleTimeout:     120 * time.Second,
        BodyLimit:       1 << 20,
        JSONEncoder:     json.Marshal,       // goccy/go-json; sonic.Marshal — ещё быстрее на amd64
        JSONDecoder:     json.Unmarshal,
        StructValidator: &sv{v: validator.New(validator.WithRequiredStructEnabled())},
        ErrorHandler: func(c fiber.Ctx, err error) error {
            var fe *fiber.Error
            code := fiber.StatusInternalServerError
            if errors.As(err, &fe) { code = fe.Code }
            return c.Status(code).JSON(fiber.Map{"error": err.Error()})
        },
    })
    app.Use(recover.New())
    app.Use(slogfiber.New(logr))
    app.Use(logger.New())

    a := &App{repo: domain.NewRepo()}

    app.Get("/health", func(c fiber.Ctx) error {
        return c.JSON(fiber.Map{"status": "ok"})
    })
    app.Get("/tasks",         a.list)
    app.Get("/tasks/:id",     a.get)
    app.Post("/tasks",        a.create)
    app.Put("/tasks/:id",     a.update)
    app.Delete("/tasks/:id",  a.del)

    go func() {
        if err := app.Listen(":8080", fiber.ListenConfig{
            EnablePrintRoutes:       true,
            GracefulShutdownTimeout: 15 * time.Second,
        }); err != nil { logr.Error("listen", "err", err) }
    }()

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    <-ctx.Done()
    sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    _ = app.ShutdownWithContext(sctx)
}

func (a *App) list(c fiber.Ctx) error {
    offset, _ := fiber.Convert(c.Query("offset", "0"), fiber.Atoi)
    limit,  _ := fiber.Convert(c.Query("limit",  "20"), fiber.Atoi)
    if limit <= 0 || limit > 100 { limit = 20 }
    return c.JSON(a.repo.List(offset, limit))
}
func (a *App) get(c fiber.Ctx) error {
    id, err := fiber.Convert(c.Params("id"), fiber.ParseInt[int64])
    if err != nil { return fiber.NewError(400, "bad id") }
    t, err := a.repo.Get(id)
    if errors.Is(err, domain.ErrNotFound) { return fiber.NewError(404, "not found") }
    return c.JSON(t)
}
func (a *App) create(c fiber.Ctx) error {
    var t domain.Task
    if err := c.Bind().JSON(&t); err != nil { return fiber.NewError(422, err.Error()) }
    slog.InfoContext(c, "creating", "title", t.Title) // c сам реализует context.Context
    return c.Status(201).JSON(a.repo.Create(t))
}
func (a *App) update(c fiber.Ctx) error {
    id, _ := fiber.Convert(c.Params("id"), fiber.ParseInt[int64])
    var t domain.Task
    if err := c.Bind().JSON(&t); err != nil { return fiber.NewError(422, err.Error()) }
    upd, err := a.repo.Update(id, t)
    if errors.Is(err, domain.ErrNotFound) { return fiber.NewError(404, "not found") }
    return c.JSON(upd)
}
func (a *App) del(c fiber.Ctx) error {
    id, _ := fiber.Convert(c.Params("id"), fiber.ParseInt[int64])
    if err := a.repo.Delete(id); errors.Is(err, domain.ErrNotFound) {
        return fiber.NewError(404, "not found")
    }
    return c.SendStatus(204)
}
```

**Что здесь важно.** `StructValidator` прописан один раз — каждый `c.Bind().JSON(&t)` автоматически выполнит валидацию после парсинга, если целевой объект — структура. Тег **`validate:"..."` (не `binding`)** — совпадает со стандартом `go-playground/validator`.

**JSON-ускорители** подключаются заменой двух функций в конфиге. `bytedance/sonic` на amd64 с AVX2 даёт 2–3× ускорение сериализации по сравнению со стандартным `encoding/json`; `goccy/go-json` — универсальный fallback, включая arm64. Эти опции есть только у Fiber «нативно» — в Gin/stdlib нужно подменять глобально.

**Graceful shutdown в Fiber v3** — `app.ShutdownWithContext(ctx)`. Есть известный edge-case (issue #3431): запросы, идущие во время shutdown, получают cancel своего `fiber.Ctx` как `context.Context`. Если вы передаёте `c` в долгую БД-операцию, она увидит `context.Canceled`. Обход — `context.WithoutCancel(c)` для критичных операций или увеличение `GracefulShutdownTimeout`.

**Тестирование через `app.Test(req)`** — Fiber использует `fasthttputil.InMemoryListener`, не поднимая реальный TCP-порт. Быстрее, чем `httptest.NewServer`, но любой инструмент, ожидающий «реальный» net/http-сервер, не подойдёт.

---

## 6. Сравнение кода бок о бок

Одна и та же операция в трёх стилях — быстрое понимание различий.

| Операция | net/http (stdlib) | Gin | Fiber v3 |
|---|---|---|---|
| Регистрация маршрута | `mux.HandleFunc("GET /tasks/{id}", h)` | `r.GET("/tasks/:id", h)` | `app.Get("/tasks/:id", h)` |
| Path-параметр | `r.PathValue("id")` | `c.Param("id")` | `c.Params("id")` |
| Query-параметр | `r.URL.Query().Get("offset")` | `c.Query("offset")` | `c.Query("offset")` |
| Парсинг JSON | `json.NewDecoder(r.Body).Decode(&v)` | `c.ShouldBindJSON(&v)` | `c.Bind().JSON(&v)` |
| Валидация | явно `validator.StructCtx(ctx,v)` | через тег `binding:"..."` | через тег `validate:"..."` + `StructValidator` |
| Ответ JSON | `json.NewEncoder(w).Encode(v)` | `c.JSON(200, v)` | `c.JSON(v)` |
| Ошибка с кодом | `http.Error(w, "msg", 404)` | `c.AbortWithStatusJSON(404,...)` | `return fiber.NewError(404,"msg")` |
| Middleware | `func(http.Handler) http.Handler` | `gin.HandlerFunc` (`c *gin.Context`) | `fiber.Handler` (`c fiber.Ctx`) |
| `context.Context` из запроса | `r.Context()` | `c.Request.Context()` | `c` сам реализует `context.Context` |
| Graceful shutdown | `srv.Shutdown(ctx)` | `srv.Shutdown(ctx)` (тот же `http.Server`) | `app.ShutdownWithContext(ctx)` |
| Тестирование | `httptest.NewServer` / `NewRecorder` | `httptest` + `r.ServeHTTP` (Gin — `http.Handler`) | `app.Test(req)` (in-memory, не httptest) |
| Тег валидации | любой, обычно `validate` | **`binding`** | **`validate`** |

Главный вывод из таблицы: **Gin и stdlib структурно похожи** (оба — `http.Handler`, оба дают `*http.Request`, оба тестируются через `httptest`), а Fiber — отдельная вселенная с собственным объектом `Ctx`, собственным тестовым рантаймом и собственной моделью жизни объектов.

---

## 7. Бенчмарки и производительность

**TechEmpower Framework Benchmarks Round 23** (опубликован 17 марта 2025 на новом железе Xeon Gold 6330, 40 Gbps Mellanox) — последний официальный раунд. **24 марта 2026 проект TFB был архивирован**, поэтому свежее официальных цифр на апрель 2026 нет; альтернативный проект — HttpArena (запущен в 2025). В Round 22 и Round 23 порядок Go-фреймворков в composite-скоре стабилен: Fiber (prefork) → Fiber → Echo → Gin → GoFrame → chi → net/http.

Порядок чисел (Round 22, отфильтрован только Go, plaintext test): Fiber — **~11.9 млн RPS**, Gin — **~5–6 млн RPS**, голый net/http — **~3–4 млн RPS**. На JSON serialization соотношение сохраняется, разрыв 2–3×. В Round 23 абсолютные числа выросли кратно благодаря новому железу, но относительные соотношения между фреймворками — почти те же.

**Самая важная оговорка — от самого Gin:** *«Fiber benchmarks use `fasthttp.RequestCtx` with per-iteration Reset, which adds constant overhead not present in net/http benchmarks. Cross-framework comparisons should be interpreted with care.»* То есть официальные числа **системно завышают Fiber на 10–20%** из-за разницы в методологии прогона бенчмарка.

**Независимые бенчмарки 2025** (Tech Tonic, Medium; buanacoding.com) на обычном оборудовании с Go 1.23, Bombardier, реалистичной нагрузкой 100–300 соединений: **Fiber ~36k RPS, Gin ~34k RPS, Echo ~34k RPS**. Median latency Fiber 2.8 ms против 3.0 ms у остальных. Разница меньше 10%.

**Почему fasthttp быстрее в микробенчмарках.** Три фактора: (1) object pooling — `RequestCtx` переиспользуется между запросами через `sync.Pool`, net/http аллоцирует новый `*http.Request`; (2) заголовки хранятся как `[]byte`, а не `map[string][]string` — нет хеширования, нет аллокации строк; (3) единый объект вместо пары `(ResponseWriter, *Request)` — меньше escape-to-heap. На коротких handler'ах это даёт 3–5× прирост.

**Почему это часто не важно в реальных приложениях.** Типичный запрос к REST API с БД: парсинг 50 мкс → middleware (auth/log) 100 мкс → **запрос к PostgreSQL 5–20 мс** → сериализация 50 мкс → отправка. При p95=10 мс, из которых 9 мс — БД, ускорение парсинга в 3× улучшает p95 с 10.000 до 9.967 мс. Это **меньше 0.4% реального выигрыша**. Автор fasthttp пишет ровно это в README: *«For most cases net/http is much better.»*

**Собственный бенчмарк через `bombardier`:**

```bash
bombardier -c 200 -d 30s -l http://localhost:8080/tasks
bombardier -c 200 -d 30s -l -m POST -H "Content-Type: application/json" \
  -b '{"title":"test"}' http://localhost:8080/tasks
```

Проверяйте **p99**, а не только avg — хвост распределения скажет больше, чем среднее. `wrk -t8 -c200 -d30s --latency http://localhost:8080/tasks` даёт распределение latency гистограммой.

**Вывод.** Для CRUD с БД, Redis-кешем или вызовами внешних API разница между тремя стеками в **real-world latency — в пределах шума** (1–5%). Абсолютный бенчмарк на Hello World — плохой прокси реальной производительности. Ранжирование «фреймворк X быстрее фреймворка Y» осмысленно только для очень узкого класса задач: API-gateway с коротким ответом, proxy, metrics-endpoint.

---

## 8. Production-соображения

### Совместимость с net/http-экосистемой

Критичная точка. **stdlib и Gin — это `http.Handler`**. На них работают: `net/http/httptest`, `otelhttp.NewHandler` (OpenTelemetry), `chi.Router` как sub-router, любой `func(http.Handler) http.Handler` middleware, grpc-gateway, Huma v2 через `humago`-адаптер, `http.FileServerFS`. **Fiber — нет**: у него собственный `fiber.Handler`, собственный тестовый API `app.Test`, собственные middleware. В v3 добавлен двунаправленный `middleware/adaptor`, но он теряет zero-copy-преимущества fasthttp.

### HTTP/2 и HTTP/3

| Стек | HTTP/2 | HTTP/3 |
|---|---|---|
| net/http (stdlib) | из коробки через TLS, ручной h2c через `x/net/http2/h2c` | экспериментально через `quic-go`, вручную |
| Gin | да, через `srv.ListenAndServeTLS()` или `UseH2C=true` | экспериментально с v1.11.0: `r.RunQUIC()` |
| Fiber (v2 и v3) | **нет** (архитектурный предел fasthttp) | **нет** |

Для Fiber официальная рекомендация — терминировать HTTP/2/3 на reverse-proxy (nginx, Cloudflare, Envoy) и проксировать HTTP/1.1 внутрь Fiber. Это **реально рабочее решение** для типичной инфраструктуры, но исключает direct-to-origin HTTP/2 от мобильных клиентов.

### context.Context

В stdlib и Gin — через `r.Context()` / `c.Request.Context()`, стандартный `context.Context`, пробрасывается в БД (`pgx.QueryContext`), `http.Client`, `slog.InfoContext`. В Fiber v2 требовался `c.UserContext()` для моста. **В Fiber v3 `fiber.Ctx` сам реализует `context.Context`** — это крупное улучшение: можно писать `db.QueryContext(c, ...)`, `slog.InfoContext(c, "...")`, и deadline/cancel/values прозрачно пробрасываются. Но с оговоркой о shutdown (см. секцию 5).

### Graceful shutdown

Во всех трёх подходах каноничный паттерн — `signal.NotifyContext` + `context.WithTimeout` + `Shutdown`. Различия минимальны. Production-best-practice — **двухфазное выключение**: сначала `readiness=false` (Kubernetes endpoints controller исключает pod из балансировки), подождать `readinessDrainDelay` (5 с), затем `srv.Shutdown(ctx)` с `shutdownPeriod` (15 с). Суммарное время должно быть меньше `terminationGracePeriodSeconds` (30 с по умолчанию), иначе придёт SIGKILL.

### Observability

**Prometheus:** stdlib — `promhttp.Handler()` из `client_golang`, ручная инструментация middleware. Gin — `zsais/go-gin-prometheus` или `penglongli/gin-metrics` (готовые метрики). Fiber v3 — `FKouhai/fiberprometheus/v3`.

**OpenTelemetry:** stdlib — `otelhttp.NewHandler(mux, "svc")`, самое чистое решение, работает с любым роутером. Gin — `otelgin.Middleware("svc")` из официального `contrib`-репозитория OTel. Fiber v3 — `github.com/gofiber/contrib/v3/otel`, переписан и переименован из v2 `otelfiber`.

### Безопасность и таймауты

`ReadHeaderTimeout` критичен — без него любой `http.Server` уязвим к Slowloris. Gosec правило G112 срабатывает автоматически. Для stdlib и Gin выставляется на `http.Server`. Для Fiber — в `fiber.Config.ReadTimeout` и `WriteTimeout`. Реальные значения из production cert-manager: `ReadHeaderTimeout=32s`, Kyverno — 30 с.

**CSRF:** Go 1.25 добавил `http.CrossOriginProtection` — встроенную защиту через Sec-Fetch-Site/Origin-заголовки, без токенов. Работает с stdlib и Gin.

### Структурированное логирование

Стандарт де-факто с 2024 — `log/slog` из stdlib (Go 1.21+). Схема для всех трёх: корневой `*slog.Logger` создаётся в `main()`, middleware добавляет request_id в derived-логгер, `slog.InfoContext(ctx, ...)` в хендлерах. Готовые обёртки: `samber/slog-gin`, `samber/slog-fiber`, для stdlib — написать самому десять строк. Секретные поля фильтруются через `slog.HandlerOptions.ReplaceAttr`.

### Тестирование

**stdlib и Gin:** `httptest.NewRecorder()` + `handler.ServeHTTP(rec, req)` — молниеносно, без сети. Альтернатива — `httptest.NewServer(handler)` для end-to-end. **Fiber:** `app.Test(req)` — свой in-memory транспорт через `fasthttputil.InMemoryListener`. Быстро, но `httptest.NewServer` и любой инструмент на его основе (`testcontainers-go`, integration-тесты с реальным портом) работают с ограничениями.

---

## 9. Когда какой стек брать

**Берите чистый net/http (Go 1.22+), если**: ваш API — CRUD без экзотики; команда ценит минимум зависимостей; нужна максимальная совместимость с экосистемой (gRPC-Gateway, OpenTelemetry, Huma); требуется HTTP/2 и вы хотите его без магии; проект — long-lived infrastructure-сервис, где важна стабильность Go release cycle и zero churn в зависимостях. Добавляйте chi, когда понадобится mount/sub-router или regex в параметрах.

**Берите Gin, если**: вам нужна эргономика (`c.JSON(200, v)`, binding через теги, группы роутов) без отказа от HTTP/2, `httptest`, OpenTelemetry и экосистемы net/http; команда привыкла к нему; есть большой пласт готовых middleware (JWT, CORS, rate-limit) под Gin. Это «безопасный средний путь» для большинства REST-сервисов. **88 тысяч звёзд и 183 тысячи импортёров — это не мода, а зрелость.**

**Берите Fiber v3, если**: сервис — высоконагруженный внутренний API-gateway или edge-сервис за reverse-proxy, который терминирует HTTP/2; вам нужны CBOR/MsgPack, кастомные JSON-энкодеры (sonic/goccy) из коробки, Express-style Req/Res для фронтенд-команды с Node-бэкграундом; вы принимаете ограничения fasthttp (мутабельные `[]byte`, нет HTTP/2, свой тестовый рантайм).

**Избегайте Fiber, если**: требуется HTTP/2 direct-to-origin; используется gRPC-Gateway или OpenAPI-first через Huma; команда завязана на ecosystem `net/http` middleware (chi, otelhttp, gorilla); критична возможность писать integration-тесты через `httptest.NewServer` с другими инструментами; нет желания разбираться с edge-case'ами graceful shutdown и временем жизни `c`.

**Альтернативы для полноты картины.** **Echo** (labstack/echo) — прямой конкурент Gin на net/http, близкий API, активный проект. **chi** — тонкий net/http-совместимый роутер (<1000 LOC), отличный выбор вместо stdlib-mux, когда нужны sub-router и регулярки. **Huma v2** (danielgtaylor/huma) — OpenAPI-first поверх любого роутера (stdlib, chi, Gin, Fiber), автоматически генерирует спеку и документацию из Go-структур, требует Go 1.25+. Если OpenAPI — требование проекта, Huma часто рациональнее, чем всё остальное.

---

## 10. Заключение

Главный нарратив этой лекции — **иерархия выбора**. Сначала отвечаете на вопрос «достаточно ли мне Go 1.22+ stdlib». С `ServeMux` pattern matching, `log/slog`, `signal.NotifyContext`, `http.CrossOriginProtection` (Go 1.25) и `otelhttp` — для большинства CRUD API ответ положительный. Только если появляются конкретные требования (group/sub-router — chi; эргономика и middleware-экосистема — Gin; узкая производительная ниша за прокси — Fiber), поднимаетесь на следующий уровень. **Фреймворки — не значение по умолчанию, а сознательный компромисс.**

Второй нарратив — **net/http vs fasthttp — это не «быстрее или медленнее», это про совместимость**. Потеря HTTP/2, `httptest`, `otelhttp`, любого middleware из net/http-экосистемы — не мелочь, а архитектурный выбор. Fiber v3 смягчил, но не отменил эти ограничения. Для подавляющего большинства сервисов, где bottleneck — БД и сеть, 10% разницы в RPS между Gin и Fiber проигрывают одной правильно добавленной БД-индексации.

Третий нарратив — **Go 1.22+ закрыл главный пробел stdlib**. До этого ServeMux был игрушкой, и chi/gorilla были почти обязательны. После — фреймворки нужны для эргономики и готовых middleware, не для «без них никак».

### Дальнейшее чтение

- Официальный блог Go: `go.dev/blog/routing-enhancements` — Jonathan Amsterdam про новый `ServeMux` (январь 2024).
- Release notes: `go.dev/doc/go1.22`, `go1.24`, `go1.25`, `go1.26` — изменения в `net/http`, `http.CrossOriginProtection`, Green Tea GC.
- Gin: `gin-gonic.com/en/blog/news/gin-1-12-0-release-announcement/` — что нового в v1.12.0.
- Fiber: `docs.gofiber.io/whats_new`, `docs.gofiber.io/blog/whats-new-in-fiber-v3` — миграция с v2 на v3.
- fasthttp: `github.com/valyala/fasthttp` — README с честными оговорками об ограничениях.
- Graceful shutdown: `victoriametrics.com/blog/go-graceful-shutdown/`, `backendbytes.com/articles/go-graceful-shutdown-production/` — паттерны для Kubernetes.
- Huma v2: `huma.rocks` — OpenAPI-first, если это ваш путь.
- OpenTelemetry: `pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` — универсальная инструментация любого `http.Handler`.

Код трёх примеров в этой лекции — не учебный минимум, а production-baseline: graceful shutdown, таймауты против Slowloris, структурированное логирование через `log/slog`, валидация, пробрасывание `context.Context`. Отталкивайтесь от него, а не упрощайте вниз.