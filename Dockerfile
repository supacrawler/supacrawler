# Stage 1: Modules caching
FROM golang:1.23.4 AS modules
WORKDIR /app
COPY go.mod .
RUN go mod download

# Stage 2: Build
FROM golang:1.23.4 AS builder
WORKDIR /app
COPY --from=modules /go/pkg /go/pkg
COPY . .
# Install playwright-go CLI matching module version
RUN PWGO_VER=$(grep -oE "playwright-go v\S+" go.mod | sed 's/playwright-go //g') \
  && go install github.com/playwright-community/playwright-go/cmd/playwright@${PWGO_VER}
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/scraper ./cmd/main.go

# Stage 3: Runtime
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
  ca-certificates tzdata libnss3 libglib2.0-0 libx11-6 libxext6 libxrender1 libxtst6 \
  libcups2 libfontconfig1 libasound2 curl && rm -rf /var/lib/apt/lists/*
COPY --from=builder /go/bin/playwright /usr/local/bin/playwright
RUN /usr/local/bin/playwright install chromium --with-deps
COPY --from=builder /bin/scraper /bin/scraper

# Copy the entrypoint script
COPY scripts/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

EXPOSE 8081
ENTRYPOINT ["/docker-entrypoint.sh"]

CMD ["/bin/scraper"]
