# Quy trình phát triển cho Fork

Repo này là **fork** của `peach0x33a/cmdcode2api`. Quy trình dưới đây giúp bạn
vừa **đồng bộ code mới từ repo gốc (upstream)** vừa **phát triển tính năng riêng**
mà không bị rối lịch sử hay xung đột triền miên.

## Mô hình branch

```
upstream/master   (repo gốc — chỉ đọc)
      │  sync định kỳ (fast-forward)
      ▼
   master          ← bản phản chiếu upstream. KHÔNG commit tay vào đây.
      │  merge
      ▼
   develop         ← branch tích hợp của BẠN (upstream + tính năng riêng)
      │
      ├── feature/xxx   ← mỗi tính năng một branch, merge vào develop qua PR
      └── fix/yyy
```

Nguyên tắc vàng:

- **`master`** luôn khớp 1:1 với `upstream/master`. Chỉ fast-forward, không commit riêng.
- **`develop`** là nơi code riêng của bạn sống. Mọi tính năng riêng merge vào đây.
- **`feature/*`**, **`fix/*`** tách ra từ `develop`, làm xong merge ngược lại.

## Remote

| Remote     | URL                                              | Vai trò              |
|------------|--------------------------------------------------|----------------------|
| `origin`   | `git@github.com:ducnm9/cmdcode2api.git`          | Fork của bạn (đọc/ghi) |
| `upstream` | `https://github.com/peach0x33a/cmdcode2api.git`  | Repo gốc (chỉ đọc)   |

Nếu clone mới, thêm lại upstream:

```bash
git remote add upstream https://github.com/peach0x33a/cmdcode2api.git
```

## Luồng 1 — Đồng bộ cái mới từ upstream

Dùng script có sẵn (khuyến nghị):

```bash
make sync
# hoặc
./scripts/sync-upstream.sh
```

Script sẽ:
1. `git fetch upstream`
2. Fast-forward `master` theo `upstream/master`, rồi push `origin master`
3. Merge `master` vào `develop`, rồi push `origin develop`
4. Nếu có **conflict** ở bước merge develop, script dừng lại và hướng dẫn bạn
   giải quyết thủ công.

Chỉ muốn cập nhật `master`, chưa đụng `develop`:

```bash
make sync-master
# hoặc
./scripts/sync-upstream.sh --no-merge
```

Làm thủ công (nếu cần hiểu rõ từng bước):

```bash
git fetch upstream
git checkout master && git merge --ff-only upstream/master
git push origin master
git checkout develop && git merge master   # xử lý conflict tại đây
git push origin develop
```

## Luồng 2 — Phát triển tính năng riêng

```bash
make feature name=ten-tinh-nang
# tương đương:
#   git checkout develop && git pull origin develop
#   git checkout -b feature/ten-tinh-nang
```

Sau khi code xong:

```bash
git push -u origin feature/ten-tinh-nang
# Mở Pull Request: feature/ten-tinh-nang  →  develop
```

Merge PR vào `develop`. Khi muốn phát hành, gộp `develop` vào bản phát hành của bạn
(hoặc tạo tag `v*` từ `develop` tùy chiến lược release).

## Xử lý conflict khi sync

Conflict xảy ra khi upstream sửa cùng chỗ bạn đã sửa trong `develop`. Khi script báo
conflict:

```bash
# 1. Xem file conflict
git status

# 2. Sửa từng file, giữ phần đúng (cả của bạn lẫn upstream)
# 3. Đánh dấu đã giải quyết
git add <file-da-sua>
git commit        # hoàn tất merge

# 4. Đẩy lên
git push origin develop
```

Mẹo giảm conflict: tính năng riêng nên đặt ở file/module tách biệt khi có thể, tránh
sửa trực tiếp vào code gốc.

## CI/CD

GitHub Actions (`.github/workflows/ci.yml`) chạy trên cả `master` và `develop`:

- **Vet & Test**: `go vet` + `go test -race` — chạy trên mọi push và PR.
- **Build**: build host + linux/amd64 — chạy trên mọi push và PR.
- **Publish to GHCR**: build & push Docker image khi push vào `master`, `develop`,
  hoặc tag `v*`.
  - `master` (default branch) → tag `latest` + `master` + `sha`
  - `develop` → tag `develop` + `sha` (KHÔNG đè `latest`)
  - tag `v1.2.3` → tag semver `1.2.3`, `1.2`, ...

Nhờ vậy bạn có image `:develop` để test bản tích hợp mà không ảnh hưởng `:latest` của
người dùng bản ổn định.

## Tóm tắt lệnh

| Việc                                | Lệnh                                   |
|-------------------------------------|----------------------------------------|
| Sync upstream → master → develop    | `make sync`                            |
| Chỉ sync master                     | `make sync-master`                     |
| Tạo branch tính năng mới            | `make feature name=xxx`                |
| Build                               | `make build`                           |
| Test                                | `make test`                            |
