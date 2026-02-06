# 多阶段构建：编译阶段
FROM golang:alpine AS builder

WORKDIR /build

# 安装依赖
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并编译
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o log-filter-monitor .

# 运行阶段
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /build/log-filter-monitor .

# 默认使用挂载的配置文件
ENTRYPOINT ["/app/log-filter-monitor"]
CMD ["-config", "/etc/log-agent/config.yaml"]
