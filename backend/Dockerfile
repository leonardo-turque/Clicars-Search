FROM golang:1.25-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

# Copy source first so go mod tidy can analyse imports (no local go.sum needed)
COPY . .
RUN go mod tidy && go build -o /server ./cmd/api/

FROM alpine:3.19

# Chromium + runtime fonts/libs required by rod for headless scraping
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    chromium \
    chromium-chromedriver \
    nss \
    freetype \
    harfbuzz \
    ttf-freefont

# Tell rod where the Chromium binary lives (alpine path)
ENV ROD_BROWSER_BIN=/usr/bin/chromium-browser \
    CHROMIUM_FLAGS="--no-sandbox --disable-dev-shm-usage --disable-gpu"

COPY --from=builder /server /server

EXPOSE 8080

CMD ["/server"]
