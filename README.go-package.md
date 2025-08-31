# Publishing a Go client package for the Scraper Engine

This guide shows how to publish a small Go client library that calls the Scraper Engine HTTP API.

## Why a separate client package?
- Stable API surface for app consumers; no dependency on engine internals
- Lightweight (no Playwright/Asynq in downstream apps)
- Versioned releases (SemVer) to communicate breaking changes clearly

## Repo layout (new repository)
```
scraper-client-go/
  go.mod
  README.md
  client/
    client.go
    types.go
```

## Module init
```bash
mkdir scraper-client-go && cd scraper-client-go
go mod init github.com/yourorg/scraper-client-go
```

## Minimal client (example)
```go
package client

import (
  "context"
  "encoding/json"
  "fmt"
  "net/http"
  "net/url"
  "strings"
  "time"
)

type Client struct {
  BaseURL string
  HTTP    *http.Client
}

func New(baseURL string) *Client {
  return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

type ScrapeParams struct {
  URL string
  Format string // markdown|html|links
  Depth int
  MaxLinks int
  IncludeSubdomains bool
  RenderJS bool
}

type ScrapeResponse struct {
  Success bool     `json:"success"`
  URL     string   `json:"url"`
  Content string   `json:"content,omitempty"`
  Links   []string `json:"links,omitempty"`
}

func (c *Client) Scrape(ctx context.Context, p ScrapeParams) (*ScrapeResponse, error) {
  q := url.Values{}
  q.Set("url", p.URL)
  if p.Format != "" { q.Set("format", p.Format) }
  if p.Depth > 0 { q.Set("depth", fmt.Sprint(p.Depth)) }
  if p.MaxLinks > 0 { q.Set("max_links", fmt.Sprint(p.MaxLinks)) }
  if p.IncludeSubdomains { q.Set("include_subdomains", "true") }
  if p.RenderJS { q.Set("render_js", "true") }

  req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/scrape?"+q.Encode(), nil)
  resp, err := c.HTTP.Do(req)
  if err != nil { return nil, err }
  defer resp.Body.Close()
  if resp.StatusCode >= 400 { return nil, fmt.Errorf("http %d", resp.StatusCode) }
  var out ScrapeResponse
  if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
  return &out, nil
}
```
Add similar methods for Jobs (`POST /v1/crawl`, `GET /v1/crawl/:id`) and Screenshots (`POST /v1/screenshots`, `GET /v1/screenshots`).

## Versioning & releases
- Tag releases with SemVer: `v0.1.0`, `v0.2.0`, ...
- Avoid breaking changes in minor versions; bump major on breaking changes

## Testing
- Table-driven tests
- For network tests, run a local Scraper Engine + Redis via docker-compose

## Why OpenAPI (option A)?
- You get consistent, multi-language SDKs (TS, Python, Go) from one spec
- Contracts are explicit; CI can verify server matches spec
- Fewer drift bugs than hand-written clients

## Why a shared types module (option C)?
- If multiple services in Go must share DTOs, a tiny `scraper-types` module keeps them in sync
- Use sparingly; avoid coupling business logic; prefer API-first in most cases

## Why not just hand-write (option B)?
- Hand-written is fine for Go-only usage and small surface area
- Tradeoff: you’ll need separate clients for TS/Python and keep them in sync manually

Recommendation: If you plan multi-language SDKs or public OSS consumption, start with OpenAPI. If Go-only and small scope, option B is acceptable and fastest.
