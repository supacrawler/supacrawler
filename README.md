# Supacrawler

Fast, reliable web scraper and crawler engine built in Go. The open-source engine powering [Supacrawler.com](https://supacrawler.com).

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

Set these environment variables:

- `HTTP_ADDR` - Server address (default: `:8081`)
- `REDIS_ADDR` - Redis address (default: `127.0.0.1:6379`)
- `DATA_DIR` - Data directory (default: `./data`)

## Development

```bash
git clone https://github.com/supacrawler/supacrawler.git
cd supacrawler
go mod tidy
go run ./cmd/main.go
```

## SDKs

- [JavaScript/TypeScript](https://github.com/supacrawler/supacrawler-js)
- [Python](https://github.com/supacrawler/supacrawler-py)

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.
