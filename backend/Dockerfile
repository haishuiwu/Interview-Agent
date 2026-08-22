# ---- Build Stage ----
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# 依赖缓存
COPY go.mod go.sum ./
RUN go mod download

# 编译
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/student-coach ./cmd/main.go

# ---- Runtime Stage ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/student-coach .
COPY --from=builder /app/data ./data

# Web 服务默认端口
EXPOSE 8080

ENTRYPOINT ["./student-coach"]
CMD ["web"]
