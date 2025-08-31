docker run -d --name redis -p 6379:6379 redis:7-alpine
docker build -t scraper:dev .
docker run --rm \
  -p 8081:8081 \
  -e REDIS_ADDR=host.docker.internal:6379 \
  -e HTTP_ADDR=":8081" \
  -v "$(pwd)/data:/app/data" \
  --name scraper \
  scraper:dev