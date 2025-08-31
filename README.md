# Supacrawler

Supacrawler's ultralight engine for scraping and crawling the web. Written in Go for maximum performance and concurrency. The open-source engine powering [Supacrawler.com](https://supacrawler.com).

A standalone HTTP service for scraping, mapping, crawling, and screenshots. It runs a web API with a background worker (Redis + Asynq). Routes match the existing Supacrawler SDKs under `/v1`.

## Quick Start

### Install via Homebrew (Recommended)

```bash
brew tap supacrawler/tap
brew install supacrawler
```

### Download Binary

Download the latest binary from the [releases page](https://github.com/supacrawler/supacrawler/releases).

### Docker

```bash
docker run --rm -p 8081:8081 ghcr.io/supacrawler/supacrawler:latest
```

## Usage

### Start the Server

```bash
# With Redis (recommended)
redis-server &
supacrawler --redis-addr=127.0.0.1:6379

# Standalone mode
supacrawler
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
 how do we do that ? go mod tidy
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

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.