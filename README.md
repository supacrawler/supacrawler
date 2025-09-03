# Supacrawler

Supacrawler's ultralight engine for scraping and crawling the web. Written in Go for maximum performance and concurrency. The open-source engine powering [Supacrawler.com](https://supacrawler.com).

A standalone HTTP service for scraping, mapping, crawling, and screenshots. It runs a web API with a background worker (Redis + Asynq). Routes match the existing Supacrawler SDKs under `/v1`.

**Why open source?** We believe powerful web scraping technology should be accessible to everyone. Whether you're a solo developer, startup, or enterprise - you shouldn't have to choose between quality and affordability. [Read our open source announcement →](https://supacrawler.com/blog/supacrawler-is-now-open-source)

## Quick Start

### Docker (Recommended)

**Option A: Docker Compose**
```bash
curl -O https://raw.githubusercontent.com/supacrawler/supacrawler/main/docker-compose.yml
docker compose up
```

**Option B: Manual Docker**
```bash
docker run -d --name redis -p 6379:6379 redis:7-alpine
docker run --rm -p 8081:8081 \
  -e REDIS_ADDR=host.docker.internal:6379 \
  ghcr.io/supacrawler/supacrawler:latest
```

### Binary Download

For advanced users who prefer native binaries:

1. **Download** from [releases page](https://github.com/supacrawler/supacrawler/releases)
2. **Install dependencies:** Redis + Node.js + Playwright v1.49.1
3. **Run:** `./supacrawler --redis-addr=127.0.0.1:6379`

**Note:** Docker is recommended for easier setup. [See complete local development guide →](https://docs.supacrawler.com/local-development)

### Requirements

**Dependencies:**
- **Redis** - for job queuing and background processing
- **Playwright** - for JavaScript rendering and screenshots

## Usage

### Start the Server

```bash
# 1. Make sure Redis is running
brew services start redis
# OR: docker run -d --name redis -p 6379:6379 redis:7-alpine

# 2. Start Supacrawler
supacrawler --redis-addr=127.0.0.1:6379
```

**What you'll see:**
```
🕷️ Supacrawler Engine
├─ Server: http://127.0.0.1:8081
├─ Health: http://127.0.0.1:8081/v1/health  
└─ API Docs: http://127.0.0.1:8081/docs
```

### API Examples

```bash
# Health check
curl http://localhost:8081/v1/health

# Scrape a webpage
curl "http://localhost:8081/v1/scrape?url=https://example.com&format=markdown"

# Take a screenshot
curl -X POST http://localhost:8081/v1/screenshots \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","full_page":true}'
```

### 📸 JavaScript Rendering & Screenshots

**This is supacrawler's core functionality** - modern web scraping requires JS rendering.

**One-line install handles this automatically.** For manual installs:

```bash
# Install Node.js and Playwright
npm install -g playwright
playwright install chromium --with-deps
```

**Without Playwright:** 
- ❌ `render_js=true` fails with "please install the driver" 
- ❌ Screenshots fail completely
- ❌ SPAs return empty content

**With Docker:** Everything works out of the box (Playwright included).

[Learn more about JavaScript rendering →](https://supacrawler.com/blog/complete-guide-scraping-javascript-websites-2025)

### Configuration

You can configure Supacrawler using environment variables or a `.env` file. Copy `.env.example` to `.env` and modify as needed.

#### Core Settings

- `HTTP_ADDR` - Server address (default: `:8081`)
- `REDIS_ADDR` - Redis address (default: `127.0.0.1:6379`)
- `DATA_DIR` - Data directory (default: `./data`)

#### Optional Settings

- `REDIS_PASSWORD` - Redis password (if required)
- `SUPABASE_URL` - Supabase project URL (for cloud storage)
- `SUPABASE_SERVICE_KEY` - Supabase service key
- `SUPABASE_STORAGE_BUCKET` - Storage bucket name (default: `screenshots`)

## Development & Contributing

**New to SupaCrawler?** [Read our comprehensive development guide →](https://docs.supacrawler.com/local-development) or [browse tutorials →](https://supacrawler.com/blog)

### Native Go Development

```bash
git clone https://github.com/supacrawler/supacrawler.git
cd supacrawler

# Copy environment template
cp .env.example .env

# Edit .env with your configuration
# Set environment variables (or use .env file)
export REDIS_ADDR=127.0.0.1:6379
export HTTP_ADDR=:8081
export DATA_DIR=./data

# Optional: enable Supabase storage upload/sign
export SUPABASE_URL=http://127.0.0.1:64321
export SUPABASE_SERVICE_KEY=<service_key>
export SUPABASE_STORAGE_BUCKET=screenshots

# Ensure Redis is running
brew services start redis
# OR: docker run -d --name redis -p 6379:6379 redis:7-alpine

# Run the server
go mod tidy
go run ./cmd/main.go
```

### Hot Reload Development (Air)

```bash
# Install Air for hot reloading
go install github.com/air-verse/air@latest

# Set environment variables (same as above)
export REDIS_ADDR=127.0.0.1:6379
export HTTP_ADDR=:8081
export DATA_DIR=./data

# Run with hot reload
air
```

### Docker Development

```bash
# Start Redis
docker run -d --name redis -p 6379:6379 redis:7-alpine

# Build and run scraper
docker build -t supacrawler:dev .
docker run --rm \
  -p 8081:8081 \
  -e REDIS_ADDR=host.docker.internal:6379 \
  -e HTTP_ADDR=":8081" \
  -e DATA_DIR="/app/data" \
  -e SUPABASE_URL="http://host.docker.internal:64321" \
  -e SUPABASE_SERVICE_KEY="<service_key>" \
  -e SUPABASE_STORAGE_BUCKET="screenshots" \
  -v "$(pwd)/data:/app/data" \
  --name supacrawler \
  supacrawler:dev
```

### One-shot Development Scripts

```bash
# Docker setup
./scripts/run.sh

# Hot reload setup
./scripts/run.sh --reload
```

## API Reference

Base URL: `http://localhost:8081/v1`

**Complete API documentation:** [docs.supacrawler.com](https://docs.supacrawler.com)

### Health Check
```bash
curl -s http://localhost:8081/internal/health
```

### Scraping
```bash
# Markdown format
curl -s "http://localhost:8081/v1/scrape?url=https://supacrawler.com&format=markdown"

# Links mapping
curl -s "http://localhost:8081/v1/scrape?url=https://supacrawler.com&format=links&depth=2&max_links=10&include_subdomains=true"
```

### Crawling
```bash
# Create crawl job
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

# Get job status
curl -s http://localhost:8081/v1/crawl/<job_id>
```

### Screenshots
```bash
# Create screenshot job
curl -s -X POST http://localhost:8081/v1/screenshots \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://supacrawler.com",
    "full_page": true,
    "format": "png",
    "width": 1366,
    "height": 768
  }'

# Get screenshot
curl -s "http://localhost:8081/v1/screenshots?job_id=<job_id>"

# Synchronous screenshot (stream to file)
curl -s -X POST http://localhost:8081/v1/screenshots \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://supacrawler.com","full_page":true,"format":"png","stream":true}' \
  --output example.png
```

## Storage Behavior

- If `SUPABASE_URL` and `SUPABASE_SERVICE_KEY` are set, images are uploaded to `SUPABASE_STORAGE_BUCKET` and a signed URL is returned.
- Otherwise, files are written under `DATA_DIR/screenshots` and served via `/files/screenshots/<name>`.

## SDKs

Use the official SDKs to integrate with your applications:

### JavaScript/TypeScript
```typescript
import { SupacrawlerClient } from '@supacrawler/js'

const client = new SupacrawlerClient({ 
  apiKey: 'anything', 
  baseUrl: 'http://localhost:8081/v1' 
})

const result = await client.scrape({ 
  url: 'https://supacrawler.com', 
  format: 'markdown' 
})
```

### Python
```python
from supacrawler import SupacrawlerClient

client = SupacrawlerClient(
  api_key='anything', 
  base_url='http://localhost:8081/v1'
)

result = client.scrape({ 
  'url': 'https://supacrawler.com', 
  'format': 'markdown' 
})
```

- [JavaScript/TypeScript SDK](https://github.com/supacrawler/supacrawler-js)
- [Python SDK](https://github.com/supacrawler/supacrawler-py)

**Tutorials & Guides:**
- [Local development setup](https://docs.supacrawler.com/local-development)
- [Python web scraping tutorial](https://supacrawler.com/blog/python-web-scraping-tutorial-for-beginners-2025)
- [JavaScript scraping guide](https://supacrawler.com/blog/complete-guide-scraping-javascript-websites-2025)
- [Best practices](https://supacrawler.com/blog/web-scraping-best-practices-avoid-getting-blocked-2025)

## Environment Variables

- `HTTP_ADDR` - Server address (default: `:8081`)
- `REDIS_ADDR` - Redis address (default: `127.0.0.1:6379`)
- `REDIS_PASSWORD` - Redis password (optional)
- `DATA_DIR` - Data directory (default: `./data`)
- `SUPABASE_URL` - Supabase project URL (optional)
- `SUPABASE_SERVICE_KEY` - Supabase service key (optional)
- `SUPABASE_STORAGE_BUCKET` - Supabase storage bucket name (optional)

## Contributing

We welcome contributions! Please see our development setup above to get started.

1. Fork the repository
2. Create a feature branch: `git checkout -b feature-name`
3. Make your changes and test locally
4. Submit a pull request

**Community Resources:**
- [Contributing guidelines](https://github.com/supacrawler/supacrawler/blob/main/CONTRIBUTING.md)
- [Development blog posts](https://supacrawler.com/blog) with technical deep dives
- [Issue tracker](https://github.com/supacrawler/supacrawler/issues) for bugs and features
- [Discussions](https://github.com/supacrawler/supacrawler/discussions) for questions and ideas

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.