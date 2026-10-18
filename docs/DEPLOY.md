# Triển khai (CI/CD → VPS qua Docker)

Luồng deploy tự động: push lên `develop` → CI build Docker image multi-arch,
publish lên **GHCR** → SSH vào VPS, `docker compose pull` ảnh mới và `up -d`
lại container. Chỉ `develop` mới deploy; các branch/tag khác không build.

```
push develop ─┬─ test (vet + go test -race)
              ├─ build (binary linux/amd64)
              ├─ docker  → publish ghcr.io/<owner>/cmdcode2api:develop
              └─ deploy  → SSH vào VPS → docker compose pull && up -d
```

File liên quan:

- `.github/workflows/ci.yml` — job `docker` (publish GHCR) và `deploy` (SSH).
- `docker-compose.prod.yml` — compose chạy trên VPS, image lấy từ biến `IMAGE`.
- `Dockerfile` — build static binary → alpine, dữ liệu ở `/data`.

## 1. Điều kiện trên VPS

Cần cài sẵn một lần:

- Docker Engine + plugin `docker compose` (v2).
- Một thư mục triển khai, ví dụ `/srv/cmdcode2api`, user deploy có quyền ghi và
  chạy được `docker` (nằm trong group `docker` hoặc dùng sudo-less setup).

```bash
# trên VPS, chạy một lần
sudo mkdir -p /srv/cmdcode2api/data
sudo chown -R "$USER" /srv/cmdcode2api
```

Thư mục `./data` giữ `config.yaml` và `usage.json`, tồn tại qua mỗi lần thay
container nên không mất account/key/usage.

## 2. GitHub Secrets cần tạo

Tạo tại **Settings → Secrets and variables → Actions → New repository secret**:

| Secret | Ý nghĩa | Ví dụ |
|--------|---------|-------|
| `VPS_HOST` | IP hoặc hostname VPS | `203.0.113.10` |
| `VPS_USER` | user SSH | `deploy` |
| `VPS_SSH_KEY` | **private key** SSH (toàn bộ nội dung) | `-----BEGIN OPENSSH PRIVATE KEY----- …` |
| `VPS_PORT` | cổng SSH | `22` |
| `VPS_PATH` | thư mục triển khai trên VPS | `/srv/cmdcode2api` |
| `GHCR_USERNAME` | username GitHub (chủ package) | `ducnm9` |
| `GHCR_TOKEN` | PAT (classic) scope `read:packages` | `ghp_…` |

Vì GHCR package để **private**, VPS phải `docker login ghcr.io` trước khi pull —
đó là lý do cần `GHCR_USERNAME` + `GHCR_TOKEN`. Token chỉ cần quyền đọc package.

### Tạo khóa SSH cho deploy

Nên dùng một cặp khóa **riêng cho CI** thay vì khóa cá nhân:

```bash
# trên máy bạn
ssh-keygen -t ed25519 -C "ci-deploy-cmdcode2api" -f ci_deploy_key
# đưa public key lên VPS
ssh-copy-id -i ci_deploy_key.pub deploy@<VPS_HOST>
# dán nội dung ci_deploy_key (private) vào secret VPS_SSH_KEY
```

### Tạo PAT đọc GHCR

GitHub → **Settings → Developer settings → Personal access tokens → Tokens
(classic)** → tạo token scope `read:packages`. Dán vào `GHCR_TOKEN`.

## 3. Khởi động lần đầu

Lần deploy đầu, `./data` trống nên container tự sinh `config.yaml` và in
`api_key` + `admin_password` **một lần** ra log:

```bash
# trên VPS
cd /srv/cmdcode2api
docker compose logs --no-log-prefix cmdcode2api | head -40
```

Ghi lại `admin_password`, mở WebUI `http://<VPS_HOST>:11434/webui`, vào tab
**Accounts** dán key Command Code (hoặc thêm qua OAuth). Trước khi có account,
chat trả `503`.

OAuth callback lắng nghe `127.0.0.1` bên trong container nên nếu dùng `--oauth`
phải chạy với host network:

```bash
docker compose run --rm --network host cmdcode2api --oauth
```

Hoặc cấu hình account ở nơi khác rồi copy `config.yaml` vào `./data/`.

## 4. Deploy thủ công (khi cần)

```bash
# trên VPS
cd /srv/cmdcode2api
echo "<GHCR_TOKEN>" | docker login ghcr.io -u <GHCR_USERNAME> --password-stdin
printf 'IMAGE=%s\n' "ghcr.io/ducnm9/cmdcode2api:develop" > .env
docker compose pull && docker compose up -d
docker logout ghcr.io
```

## 5. Khi nào deploy chạy

Chỉ **push lên `develop`** mới build image và deploy:

- Push `develop` → publish image `:develop` → deploy tự động lên VPS.
- Pull request → chỉ chạy test (`vet` + `go test`), không build image, không deploy.
- `master` và các branch/tag khác → **không** build, không deploy (tiết kiệm tài nguyên; `master` chỉ phản chiếu upstream).

Mỗi lần build develop còn tạo thêm tag bất biến `:sha-<commit>` để rollback.

## 6. Rollback

Deploy không giữ bản cũ tự động. Để quay lại một phiên bản đã publish:

```bash
# trên VPS
cd /srv/cmdcode2api
printf 'IMAGE=%s\n' "ghcr.io/ducnm9/cmdcode2api:sha-<commit>" > .env
docker compose pull && docker compose up -d
```

Các tag `sha-<commit>` do CI tạo sẵn cho mỗi lần build (xem `docker/metadata-action`).
