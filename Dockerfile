FROM node:24-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS api-build
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X kanpic/internal/buildinfo.Version=${VERSION} -X kanpic/internal/buildinfo.Commit=${COMMIT} -X kanpic/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/kanpic ./cmd/api

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S kanpic && adduser -S -G kanpic -u 10001 kanpic
WORKDIR /app
COPY --from=api-build /out/kanpic /app/kanpic
COPY --from=web-build /src/web/dist /app/web
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/kanpic"]
