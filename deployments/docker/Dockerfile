# 构建单微服务镜像：SERVICE_PATH 指向 main 包目录，如 cmd/gateway。
# 示例：docker build -f deployments/docker/Dockerfile --build-arg SERVICE_PATH=cmd/gateway -t pim/gateway:latest .
FROM golang:1.25-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE_PATH=cmd/gateway
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/service "./${SERVICE_PATH}"

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/service /service
USER 65532:65532
ENTRYPOINT ["/service"]
