# AGENTS.md

Hướng dẫn cho AI coding agent (và lập trình viên mới) làm việc trên repo này.
Đọc file này trước khi sửa code. Tài liệu kiến trúc chi tiết ở
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md); quy trình fork/branch ở
[`docs/WORKFLOW.md`](docs/WORKFLOW.md).

## Dự án là gì

`cmdcode2api` là một **gateway tương thích OpenAI** cho dịch vụ
[Command Code](https://commandcode.ai/). Client theo chuẩn OpenAI gọi
`/v1/chat/completions` và `/v1/models`; gateway chuyển tiếp sang API nội bộ của
Command Code (`/alpha/generate`), xoay vòng nhiều tài khoản, theo dõi usage, và
phục vụ một WebUI quản trị nhúng.

Lưu ý quan trọng: **"cc" = Command Code, KHÔNG phải Claude Code.** Dự án từng tên
`cc-gateway`, đổi tên để tránh nhầm. Mọi tham chiếu `cc`, `CC`, `CCClient`,
`x-command-code-version`... đều nói về Command Code.

## Ngăn xếp & phụ thuộc

- Go **1.25** (xem `go.mod`).
- Phụ thuộc runtime **duy nhất**: `gopkg.in/yaml.v3`. Phần còn lại chỉ dùng
  thư viện chuẩn.
- **Giữ nguyên nguyên tắc này**: không thêm dependency mới trừ khi thật sự cần
  và được thống nhất trước. Ưu tiên thư viện chuẩn.

## Lệnh build / test / chạy

Dùng `Makefile` ở gốc:

```bash
make build      # go build -o cmdcode2api ./cmd/cmdcode2api
make test       # go test -v ./...
make run        # build rồi chạy
make build-linux
make clean
```

CI (`.github/workflows/ci.yml`) chạy, và bạn NÊN chạy trước khi commit:

```bash
go vet ./...
go test -race -count=1 ./...
go build -trimpath ./cmd/cmdcode2api
```

**Luôn chạy `go vet` + `go test -race` sau khi sửa code** và sửa hết lỗi trước
khi báo xong việc. Dự án có bộ test khá dày trong `internal/app/*_test.go` —
chạy chúng là cách xác minh chính.

Định dạng: dùng `gofmt`/`gofumpt` chuẩn Go (tab, không space). Đừng tự ý đổi
style.

## Cấu trúc thư mục

```text
cmd/cmdcode2api/main.go   Entry point mỏng, chỉ gọi app.Run()
internal/app/             Toàn bộ logic gateway (xem bảng dưới)
internal/i18n/            Dịch message admin theo Accept-Language (EN là nguồn)
internal/web/             WebUI nhúng — web.go + index.html (go:embed)
scripts/                  deploy.sh, sync-upstream.sh
docs/                     ARCHITECTURE.md, WORKFLOW.md
```

Các file chính trong `internal/app/`:

| File | Vai trò |
|------|---------|
| `app.go` | `Run()`: parse flags, bootstrap, chế độ `--oauth`, startup |
| `server.go` | HTTP server, routing, middleware chain, graceful shutdown, quota loop |
| `handler.go` | Handler chat completions (stream + non-stream) & models; SSE→OpenAI |
| `cc.go` | **CCClient** upstream: rotation/failover, chuyển đổi OpenAI↔CC, parse SSE |
| `config.go` | Schema `Config`, load/save YAML, migrate field legacy |
| `accounts.go` | `Account` + `AccountPool` (round-robin, cooldown, views) |
| `clientkeys.go` | `ClientKeyPool` — bearer key cục bộ của client |
| `oauth.go` | Luồng OAuth browser (CLI + WebUI), parse callback link |
| `models.go` | `modelCatalog` toàn cục, fetch `/provider/v1/models`, exclude filter |
| `types.go` | Data model hai chiều: OpenAI và Command Code |
| `quota.go` | `QuotaService` + snapshot từ các endpoint `/alpha/*` |
| `usage.go` | `UsageTracker`: bộ đếm atomic, persist `usage.json` |
| `admin.go` | Admin JSON API `/admin/api/*` |
| `events.go` | `CCEventNormalizer`: chuẩn hóa SSE thành text/reasoning/toolcall/finish |
| `logging.go`, `logbuf.go` | Format log màu + ring buffer cho WebUI |
| `ratelimit.go` | `ipRateLimiter` cho admin login |
| `random.go` | Sinh key/password/hex |

## Quy ước & phong cách code

- **Comment giải thích "tại sao", không phải "làm gì".** Code hiện có có nhiều
  comment nêu lý do một quyết định (vd tại sao `readSSELine` không dùng
  `bufio.Scanner`). Giữ phong cách này: khi viết logic không hiển nhiên, ghi lại
  lý do.
- Comment trong code viết bằng **tiếng Anh**. Message hướng tới người dùng
  (admin API) viết bằng tiếng Anh làm nguồn, rồi để i18n dịch — xem mục i18n.
- Lỗi trả cho client phải theo **shape error của OpenAI** (`writeError`,
  `writeErrorWithCode`). Đừng rò rỉ lỗi nội bộ thô.
- Dùng typed error (`invalidRequestError`, `upstreamAPIError`,
  `quotaHTTPError`) + `errors.As` để phân loại; đừng so khớp chuỗi lỗi.
- Trạng thái dùng chung được bảo vệ bằng mutex (`Config.mu`, `Account.mu`,
  `CCClient.baseURLMu`) hoặc `atomic`. Giữ nguyên khi sửa — chú ý data race
  (CI chạy `-race`).

## i18n (đa ngôn ngữ)

- Hỗ trợ **tiếng Anh (en)** và **tiếng Việt (vi)**. EN là nguồn và fallback.
- Message admin viết bằng **tiếng Anh tại call site**; `i18n.Message(lang, "...")`
  dịch sang ngôn ngữ của request (`i18n.FromRequest(r)` đọc `Accept-Language`).
- **EN là nguồn và fallback** — chuỗi EN đi qua i18n không đổi; chỉ cần thêm bản
  dịch tiếng Việt trong catalog `VI`.
- Khi thêm message admin mới hướng tới người dùng, thêm bản dịch tiếng Việt tương
  ứng trong `internal/i18n/i18n.go` thay vì hardcode một ngôn ngữ.
- WebUI (`internal/web/index.html`) có hệ thống i18n riêng phía frontend với bảng
  `en` và `vi` (key dạng `data-i18n="..."`). Thêm chuỗi UI mới phải cập nhật cả
  hai bảng.
- **Không dùng tiếng Trung** trong code, comment, UI hay tài liệu. Comment code
  viết bằng tiếng Anh.

## Cạm bẫy (gotchas) — đọc kỹ trước khi sửa

1. **Account ID bắt nguồn từ API key** (`accountID = "a" + hex(SHA256(key)[:4])`).
   Đổi key của một account = **đổi ID**. Khi `SetKey`, phải di chuyển bộ đếm usage
   (`UsageTracker.MoveAccount`) và quota, nếu không số liệu sẽ "mồ côi".
2. **Failover chỉ trước khi ghi byte đầu cho client.** Một khi stream đã bắt đầu
   gửi, KHÔNG được replay sang account khác. Đừng phá bất biến này trong
   `handler.go`.
3. **`shouldFailover`** quyết định lỗi nào đáng thử account khác (401/403/429/5xx,
   lỗi transport) và lỗi nào KHÔNG (400/422 bad request, context canceled). Sửa
   logic retry phải cập nhật hàm này cho nhất quán.
4. **Command Code chỉ hỗ trợ streaming.** `openAIToCC` luôn đặt `Stream: true`;
   chế độ non-stream của client được dựng lại bằng cách gom toàn bộ SSE
   (`handleNonStream`).
5. **Endpoint `/alpha/*` là KHÔNG chính thức** (từ cộng đồng commandcode-usage).
   Parser quota cố ý "tolerant": camelCase/snake_case, epoch giây/mili/ISO, phản
   hồi phẳng hoặc bọc trong `data`. Giữ tính phòng thủ này; đừng giả định một
   shape cố định.
6. **Headers CLI phải khớp client thật.** `setCCHeaders` gửi
   `x-command-code-version` (`ccCLIVersion`), `x-cli-environment`, `User-Agent`
   giống CLI chính thức. Cả đường generate và quota đều đi qua đây để danh tính
   nhất quán.
7. **`readSSELine` cố tình KHÔNG dùng `bufio.Scanner`** vì một tool-call event có
   thể rất lớn (hàng trăm KB). Đừng thay bằng Scanner.
8. **Server chạy được với 0 account** — chat trả `503 no_accounts` cho tới khi
   thêm account. Đừng thêm fail-fast lúc startup vì thiếu account.
9. **`config.yaml` và `usage.json` KHÔNG commit** (đã gitignore) và lưu quyền
   `0600`. Đừng in giá trị key/password ra log (trừ dòng in-một-lần có chủ đích
   lúc khởi tạo).

## Khi thêm/sửa HTTP endpoint

- Route công khai + OpenAI đăng ký trong `server.go` `runServer()`.
- Route admin đăng ký trong `admin.go` `registerAdminRoutes()` (nằm sau
  `adminAuth`).
- Nhớ chuỗi middleware: `cors → logging → securityHeaders → authMiddleware → mux`.
- Thêm path công khai mới (không cần Bearer) thì cập nhật `isPublicPath`.
- Sau khi đổi API, **cập nhật README.md** (phần HTTP API / Admin API) và
  `docs/ARCHITECTURE.md` nếu ảnh hưởng luồng.

## Repo là fork

Đây là fork của `peach0x33a/cmdcode2api`. Phát triển trên branch `develop`,
không commit tay vào `master` (master phản chiếu upstream). Đồng bộ bằng
`make sync`. Chi tiết: [`docs/WORKFLOW.md`](docs/WORKFLOW.md).

## Checklist trước khi báo xong việc

- [ ] `go vet ./...` sạch
- [ ] `go test -race -count=1 ./...` xanh
- [ ] `go build -trimpath ./cmd/cmdcode2api` thành công
- [ ] Không thêm dependency ngoài ý muốn (`go.mod`/`go.sum` không phình)
- [ ] Message hướng tới người dùng đã qua i18n
- [ ] Cập nhật README/docs nếu đổi hành vi hoặc API
- [ ] Không rò rỉ secret ra log
