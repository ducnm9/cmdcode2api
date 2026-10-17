# Kiến trúc `cmdcode2api`

Tài liệu kiến trúc nội bộ cho lập trình viên và AI agent. Hướng dẫn sử dụng và
cấu hình nằm ở [`README.md`](../README.md); quy ước code ở
[`AGENTS.md`](../AGENTS.md).

## 1. Tổng quan

`cmdcode2api` là một gateway **tương thích OpenAI** đặt trước dịch vụ
[Command Code](https://commandcode.ai/). Client dùng chuẩn OpenAI gọi gateway;
gateway dịch request sang định dạng Command Code, chuyển tiếp tới
`/alpha/generate`, rồi dịch SSE trả về thành phản hồi OpenAI.

```
┌────────────┐   OpenAI format    ┌──────────────┐   CC format     ┌────────────────┐
│  Client    │ ─────────────────► │  cmdcode2api │ ──────────────► │  Command Code  │
│ (OpenAI    │   /v1/chat/...     │   gateway    │  /alpha/generate│  upstream API  │
│  SDK/curl) │ ◄───────────────── │              │ ◄────────────── │                │
└────────────┘   chunk/JSON       └──────────────┘   SSE events    └────────────────┘
                                        │
                                        ├── WebUI /webui (admin)
                                        ├── Quota loop → /alpha/{whoami,billing,usage}
                                        └── Persistence: config.yaml, usage.json
```

Đặc điểm thiết kế:
- Binary Go đơn, phụ thuộc runtime duy nhất `gopkg.in/yaml.v3`.
- Trạng thái runtime trong bộ nhớ; chỉ `config.yaml` và `usage.json` được lưu
  xuống đĩa.
- "Personal utility gateway" — bám theo shape API Command Code quan sát được.

> **Thuật ngữ:** "cc" trong code = **Command Code**, không phải Claude Code.

## 2. Khởi động (`app.go` → `Run()`)

```
main.go → app.Run()
  ├─ parse flags: --oauth --oauth-callback --host --port --debug --version
  ├─ findConfig() → "config.yaml"
  ├─ nếu --oauth:  loadConfig → runOAuth → pool.Add(account) → saveConfig → thoát
  └─ chế độ thường:
       ├─ loadConfig()
       │    └─ nil (lần đầu) → defaultConfig() → writeConfigTemplate() → in 1 lần → thoát
       ├─ sinh admin_password nếu rỗng
       ├─ áp default: port 11434, host localhost, base_url https://api.commandcode.ai
       ├─ flag --host/--port/--debug override
       ├─ log → io.MultiWriter(stderr, ring)         # ring buffer cho WebUI
       ├─ NewAccountPool / NewCCClientWithPool / loadUsage()
       ├─ FetchProviderModels() nếu có account enabled
       └─ runServer(cc, cfg, usage, ring)
```

Các mặc định quan trọng: `Version = "v0.2.0"` (`app.go`), port `11434`, host
`localhost`, base URL `https://api.commandcode.ai`.

## 3. HTTP server & middleware (`server.go`)

`runServer()` dựng `http.ServeMux` và bọc chuỗi middleware (áp từ ngoài vào):

```
corsMiddleware → loggingMiddleware → securityHeaders → authMiddleware → mux
```

- **corsMiddleware** — `Access-Control-Allow-*`, trả 204 cho preflight OPTIONS.
- **loggingMiddleware** — log mỗi request (trừ `/health`) kèm IP đã resolve.
- **securityHeaders** — CSP, `X-Frame-Options: DENY`, `nosniff`,
  `Referrer-Policy: no-referrer`.
- **authMiddleware** — bỏ qua public path (`isPublicPath`: `/health`, `/usage`,
  `/webui*`, `/admin/*`, OPTIONS). Path khác cần `Authorization: Bearer <key>`;
  tra `ClientKeyPool.Lookup`, kiểm tra enabled, nhét client-key ID vào context.

Admin có lớp auth riêng (`adminAuth`) dùng `admin_password`, rate-limit theo IP.

Server timeouts: Read 30s, **Write 600s** (cho streaming), Idle 120s. Graceful
shutdown qua SIGINT/SIGTERM (timeout 15s). Vòng lặp quota chạy nền, dừng theo
context khi shutdown.

### Bảng route

| Method | Path | Auth | Handler |
|--------|------|------|---------|
| GET | `/health` | không | inline `{"status":"ok"}` |
| GET | `/usage` | không | `usage.Snapshot()` |
| GET | `/v1/models` | Bearer client key | `handleModels` |
| POST | `/v1/chat/completions` | Bearer client key | `handleChatCompletions` |
| * | `/webui`, `/webui/` | không (public) | `web.Handler()` (nếu bật WebUI) |
| * | `/admin/*` | admin_password | `adminMux` qua `adminAuth` |
| POST | `/admin/api/oauth/callback` | state token | `handleWebOAuthCallback` (ngoài adminAuth) |

Admin API (`admin.go` `registerAdminRoutes`): overview; accounts CRUD + test +
quota refresh; models get/put; keys CRUD + reveal; settings get/put; logs; oauth
start/status/complete/cancel. Chi tiết payload xem README mục Admin API.

## 4. Luồng một request chat (`handler.go` + `cc.go`)

```
handleChatCompletions
  ├─ MaxBytesReader 50MB → decode ChatRequest
  ├─ validate: model != "" , không bị exclude (404 nếu exclude), messages != []
  ├─ cc.Send(ctx, req)
  │    ├─ openAIToCC(req)                      # chuyển định dạng (mục 5)
  │    ├─ nếu EnabledCount()==0 → 503 no_accounts
  │    └─ vòng lặp theo số account enabled:
  │         ├─ acct = pool.Acquire()           # round-robin, bỏ disabled/cooldown
  │         ├─ doSend() POST /alpha/generate   # header CLI
  │         ├─ ok   → acct.RecordSuccess(); return (resp, acct)
  │         └─ lỗi  → acct.RecordFailure(err)
  │                   shouldFailover? → thử account kế / return
  │         (hết account cooldown → 429 kèm EarliestRateLimitWait)
  └─ theo req.Stream:
       ├─ true  → handleStreamWithOptions   # SSE CC → chunk OpenAI, [DONE]
       └─ false → handleNonStream           # gom SSE → 1 ChatResponse
     → usage.Record(...) qua RecorderFor(acct, clientKeyID)
     → usage.save()
```

**Bất biến quan trọng:** failover chỉ xảy ra **trước khi ghi byte đầu tiên** cho
client. Khi stream đã bắt đầu, không bao giờ replay sang account khác.

`shouldFailover`:
- Thử account khác: 401, 403, 429, 5xx, lỗi transport.
- Không thử: 400/422 (bad request), `context.Canceled`,
  `context.DeadlineExceeded`, `invalidRequestError`.

429 đẩy account vào cooldown theo `Retry-After` (mặc định 60s,
`defaultRateLimitCooldown`).

## 5. Chuyển đổi định dạng (`cc.go`, `types.go`)

`openAIToCC(req)` dựng `CCRequest`:
- `resolveModelName`: giữ nguyên nếu có prefix `provider/`; nếu không, tra
  `modelCatalog`; cuối cùng suy prefix theo tên (`gemini-`→`google/`,
  `claude-`→`anthropic/`, `gpt-`→`openai/`).
- `messagesToCC`, `toolsToCC`, `extractSystem` chuyển messages/tools/system.
- Image: nhiều shape (OpenAI `image_url` object/string, `input_image`, Anthropic
  `image` block) → image block CC; chỉ nhận base64 inline, URL remote bị từ chối
  `400`.
- `MaxTokens` = `OutputTokenBudget()` (ưu tiên `max_completion_tokens`, rồi
  `max_tokens`); mặc định `defaultCCMaxTokens = 64_000`, trần
  `maximumCCMaxTokens = 200_000`.
- `Stream: true` luôn (Command Code chỉ hỗ trợ stream).

Chiều ngược lại: SSE của CC được đọc dòng-từng-dòng bằng `readSSELine` (không
dùng `bufio.Scanner` vì event tool-call có thể rất lớn — trần `maxSSELineBytes =
32MB`), decode thành `CCStreamEvent`, rồi `CCEventNormalizer` (`events.go`)
chuẩn hóa thành bốn loại: text, reasoning, tool-call, finish. Handler phát lại
dưới dạng chunk OpenAI (stream) hoặc gom thành `ChatResponse` (non-stream).

`setCCHeaders` gắn danh tính CLI trên mọi request upstream (generate + quota):
`Authorization: Bearer`, `x-command-code-version` (`ccCLIVersion = "1.54.2"`),
`x-cli-environment = "production"`, `User-Agent = "cli"`.

## 6. Account pool & client keys (`accounts.go`, `clientkeys.go`)

`AccountPool` giữ các `Account` (credential upstream Command Code):
- `Acquire()` xoay vòng round-robin, bỏ account disabled hoặc đang cooldown.
- Mỗi `Account` có trạng thái runtime: `Errors` (atomic), `lastError`,
  `rateLimitedUntil`, `authFailures` (ngưỡng `authErrorThreshold = 3` để đánh dấu
  nghi ngờ nhưng vẫn giữ enabled).
- **ID = `"a" + hex(SHA256(apiKey)[:4])`** → ổn định qua đổi tên, không lộ key
  thô. Đổi key = đổi ID → phải `UsageTracker.MoveAccount` để giữ bộ đếm.
- `Views()` cho admin API (không bao giờ chứa key thô; chỉ `MaskedKey`).

`ClientKeyPool` tương tự nhưng cho bearer key cục bộ của client (prefix `ccgw-`,
luôn do server sinh, có mask/reveal). Dùng để xác thực request vào gateway và
tính usage theo từng key.

## 7. Quota service (`quota.go`)

`QuotaService` đọc hạn mức từ các endpoint **không chính thức** `/alpha/*`:
`whoami` (org id) → `billing/credits` (số dư + cửa sổ 5h/tuần) →
`billing/subscriptions` (gói + chu kỳ) → `usage/summary` (tổng hợp chu kỳ).

- Chạy một lần ngay sau khởi động, rồi **mỗi 5 phút** (`quotaRefreshInterval`),
  tối đa `quotaRefreshWorkers = 4` request đồng thời.
- Parser **tolerant**: camelCase/snake_case, epoch giây/mili/ISO, phản hồi phẳng
  hoặc bọc trong `data` (do endpoint không chính thức, field hay thay đổi).
- Monthly cap là **ước lượng** từ `knownPlans` (vd `individual-pro`→30); gói lạ
  → chỉ hiển thị số dư.
- Lỗi một phần ghi vào `Failures`; nếu query trả rỗng, giữ snapshot cũ và chỉ cập
  nhật `LastError`/`LastChecked`.
- Snapshot cache trong `usage.json` (sống qua restart), KHÔNG lộ ở `/usage`.

## 8. Usage tracking (`usage.go`)

`UsageTracker` đếm bằng atomic ở ba cấp: toàn cục, theo account (`accounts[id]`),
theo client key (`client_keys[id]`). Mỗi chat ghi `prompt/completion/cache_read/
cache_write` tokens. Persist `usage.json` ghi atomic (`.tmp` + rename). Cung cấp
`MoveAccount`/`DropAccount` khi key account đổi. Map
`inputTokenDetails.cacheReadTokens` của CC → `prompt_tokens_details.cached_tokens`
trong phản hồi OpenAI.

## 9. OAuth (`oauth.go`)

Hai đường cùng dùng `OAuthFlow`:
- **CLI** (`--oauth`): bind listener `127.0.0.1:5959-5968`, in URL, `Wait()`.
- **WebUI**: chế độ `NoLocalListener`; trang Command Code POST credential về
  `/admin/api/oauth/callback`, hoặc người dùng dán link redirect
  (`ParseCallbackURL`).

Bảo vệ bằng **state token** (CSRF) sinh ngẫu nhiên, dùng một lần, phải khớp flow
đang chờ. Gateway **không bao giờ fetch** link người dùng dán — chỉ parse query
cục bộ. Mỗi flow thành công **append** một account; key trùng là no-op.

## 10. WebUI & i18n

- `internal/web/web.go` nhúng `index.html` qua `go:embed`, phục vụ ở `/webui`.
  Là một file HTML single-file; cũng có thể mở trực tiếp và trỏ vào instance bất
  kỳ.
- `internal/i18n/i18n.go` dịch message admin theo `Accept-Language`
  (`i18n.FromRequest`). **EN là nguồn và fallback**; chỉ bản copy trả về admin
  được localize, snapshot lưu trữ giữ nguyên tiếng Anh.

## 11. Cấu hình & persistence

- `config.yaml` (quyền `0600`, gitignore): `api_keys`, `admin_password`, `webui`,
  `host`, `port`, `commandcode.{base_url,accounts}`, `exclude_models`. Field
  legacy đơn (`api_key`, `commandcode.api_key`) được **migrate** sang list khi
  load và xóa khi save. Getter/setter cho các field admin sửa lúc chạy
  (`ExcludeModels`, `BaseURL`, `AdminPassword`) được bảo vệ bằng `Config.mu`.
- `usage.json` (gitignore): bộ đếm usage + snapshot quota cache.
- Dự án **không** đọc biến môi trường cho cấu hình chính — tất cả qua
  `config.yaml` + flags CLI.

## 12. Triển khai

- **Dockerfile**: build tĩnh (`CGO_ENABLED=0`), image `alpha:3`. `config.yaml` và
  `usage.json` ở volume `/data`. `EXPOSE 11434`, `CMD ["--host","0.0.0.0"]`. Build
  đổi proxy qua `--build-arg GOPROXY=...`.
- **docker-compose.example.yml**: copy thành `docker-compose.yml`, map
  `11434:11434`, volume `./data:/data`.
- **CI** (`.github/workflows/ci.yml`): vet + test (`-race`) + build trên push
  `master`/`develop` và PR; publish Docker lên GHCR cho `master` (`latest`),
  `develop` (`:develop`) và tag `v*`.
- OAuth trong container cần host network vì listener bind `127.0.0.1` trong
  namespace container — xem README.

## 13. Chạy sau reverse proxy

Server chỉ tin `CF-Connecting-IP` / `X-Forwarded-For` từ kết nối proxy loopback;
chỉ dùng cho log HTTP và rate-limit admin login. Với `X-Forwarded-For` chỉ lấy
entry phải nhất. Kết nối trực tiếp với header giả vẫn dùng địa chỉ TCP peer. Chi
tiết cấu hình nginx/Cloudflare ở README.

## Sơ đồ phụ thuộc giữa các thành phần

```
Run (app.go)
  └─ runServer (server.go)
       ├─ CCClient (cc.go) ──► AccountPool (accounts.go)
       │     └─ openAIToCC/parseSSE (cc.go) ──► CCEventNormalizer (events.go)
       ├─ ClientKeyPool (clientkeys.go)
       ├─ UsageTracker (usage.go)
       ├─ QuotaService (quota.go) ──► CCClient, AccountPool, UsageTracker
       ├─ admin routes (admin.go) ──► tất cả + i18n
       ├─ web.Handler (internal/web)
       └─ middleware + ipRateLimiter (ratelimit.go) + logRing (logbuf.go)
```
