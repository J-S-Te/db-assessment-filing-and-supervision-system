FROM node:24.18.0-bookworm-slim AS web-build
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack pnpm install --frozen-lockfile
COPY web/ ./
RUN corepack pnpm build

FROM golang:1.26.4-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY migrations/ ./migrations/
RUN CGO_ENABLED=0 go build -trimpath -o /out/grade-business ./cmd/manager

FROM debian:bookworm-slim
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates poppler-utils poppler-data tesseract-ocr \
        tesseract-ocr-chi-sim tesseract-ocr-eng tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 10001 grade-business \
    && useradd --system --uid 10001 --gid 10001 --home-dir /app --no-create-home grade-business \
    && install -d -o grade-business -g grade-business /var/lib/grade-business/attachments
WORKDIR /app
COPY --from=go-build /out/grade-business /usr/local/bin/grade-business
COPY --from=web-build /src/web/dist/ /app/web/
ENV GRADE_BUSINESS_WEB_DIR=/app/web \
    GRADE_BUSINESS_ATTACHMENT_DIR=/var/lib/grade-business/attachments
USER 10001:10001
EXPOSE 18444
ENTRYPOINT ["/usr/local/bin/grade-business"]
