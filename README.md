# Supacrawler
Supacrawler's ultralight engine for scraping and crawling the web. Written in go for maximum performance and concurrency. 

A standalone HTTP service for scraping, mapping, crawling, and screenshots. It runs a web API with a background worker (Redis + Asynq). Routes match the existing Supacrawler SDKs under `/v1`.

## 1) Install and run

### Homebrew (macOS/Linux)
```bash
brew tap yourorg/tap
brew install scraper
brew services start scraper
```
Defaults:
- HTTP_ADDR=:8081
- REDIS_ADDR=127.0.0.1:6379
- DATA_DIR=$HOMEBREW_PREFIX/var/scraper

Override:
```bash
brew services stop scraper
HTTP_ADDR=":8081" REDIS_ADDR="127.0.0.1:6379" DATA_DIR="$HOME/.scraper" \
SUPABASE_URL="http://127.0.0.1:64321" \
SUPABASE_SERVICE_KEY="<service_key>" \
SUPABASE_STORAGE_BUCKET="screenshots" \
brew services start scraper
```

### Docker (recommended for local)
```bash
cd apps/scraper
# start redis then scraper
docker run -d --name redis -p 6379:6379 redis:7-alpine
docker build -t scraper:dev .
docker run --rm \
  -p 8081:8081 \
  -e REDIS_ADDR=host.docker.internal:6379 \
  -e HTTP_ADDR=":8081" \
  -e DATA_DIR="/app/data" \
  -e SUPABASE_URL="http://host.docker.internal:64321" \
  -e SUPABASE_SERVICE_KEY="<service_key>" \
  -e SUPABASE_STORAGE_BUCKET="screenshots" \
  -v "$(pwd)/data:/app/data" \
  --name scraper \
  scraper:dev
```

### Native Go
```bash
cd apps/scraper
export REDIS_ADDR=127.0.0.1:6379
export HTTP_ADDR=:8081
export DATA_DIR=./data
# Optional: enable Supabase storage upload/sign
export SUPABASE_URL=http://127.0.0.1:64321
export SUPABASE_SERVICE_KEY=<service_key>
export SUPABASE_STORAGE_BUCKET=screenshots
# ensure Redis is running (brew services start redis OR docker run redis)

go mod tidy
go run ./cmd/main.go
```

### Hot reload (Air)
- Install Air:
```bash
go install github.com/air-verse/air@latest
```
- Config file already included: `apps/scraper/.air.toml`
- Run with hot reload:
```bash
cd apps/scraper
export REDIS_ADDR=127.0.0.1:6379
export HTTP_ADDR=:8081
export DATA_DIR=./data
# Optional Supabase
export SUPABASE_URL=http://127.0.0.1:64321
export SUPABASE_SERVICE_KEY=<service_key>
export SUPABASE_STORAGE_BUCKET=screenshots
air
```

### One-shot script
```bash
cd apps/scraper
# docker
./scripts/run.sh
# hot reload (air)
./scripts/run.sh --reload
```

## 2) Test the API (curl)
Base URL: `http://localhost:8081/v1`

- Health
```bash
curl -s http://localhost:8081/internal/health
```
- Scrape (markdown)
```bash
curl -s "http://localhost:8081/v1/scrape?url=https://supacrawler.com&format=markdown"
```
- Map (links)
```bash
curl -s "http://localhost:8081/v1/scrape?url=https://supacrawler.com&format=links&depth=2&max_links=10&include_subdomains=true"
```
- Create crawl job
```bash
curl -s -X POST http://localhost:8081/v1/crawl \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://supacrawler.com",
    "type": "crawl",
    "format": "markdown",
    "depth": 2,
    "link_limit": 20,
    "include_subdomains": true,
    "render_js": false,
    "include_html": false
  }'
```
- Get job status
```bash
curl -s http://localhost:8081/v1/crawl/<job_id>
```
- Create screenshot job
```bash
curl -s -X POST http://localhost:8081/v1/screenshots \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://supacrawler.com",
    "full_page": true,
    "format": "png",
    "width": 1366,
    "height": 768
  }'
```
- Get screenshot
```bash
curl -s "http://localhost:8081/v1/screenshots?job_id=<job_id>"
```
- Synchronous screenshot (stream to file)
```bash
curl -s -X POST http://localhost:8081/v1/screenshots \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://supacrawler.com","full_page":true,"format":"png","stream":true}' \
  --output example.png
```

## 3) Storage behavior
- If `SUPABASE_URL` and `SUPABASE_SERVICE_KEY` are set, images are uploaded to `SUPABASE_STORAGE_BUCKET` and a signed URL is returned in GET.
- Otherwise, files are written under `DATA_DIR/screenshots` and served via `/files/screenshots/<name>`.

## 4) SDK configuration
- JavaScript
```ts
import { SupacrawlerClient } from '@supacrawler/js'
const client = new SupacrawlerClient({ apiKey: 'anything', baseUrl: 'http://localhost:8081/v1' })
const res = await client.scrape({ url: 'https://supacrawler.com', format: 'markdown' })
```
- Python
```python
from supacrawler import SupacrawlerClient
client = SupacrawlerClient(api_key='anything', base_url='http://localhost:8081/v1')
res = client.scrape({ 'url': 'https://supacrawler.com', 'format': 'markdown' })
```

## 5) Notes
- Env vars: HTTP_ADDR, REDIS_ADDR, REDIS_PASSWORD, DATA_DIR, SUPABASE_URL, SUPABASE_SERVICE_KEY, SUPABASE_STORAGE_BUCKET
- Artifacts under DATA_DIR are served at `/files/*`
- Windows: use Docker or native binary + service wrapper (NSSM)
