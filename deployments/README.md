# Deployments 使用说明

这里提供两条运行方式（按需二选一）：

- 路径 A：`Docker Compose` 启中间件 + 本地 `go run` 业务服务
- 路径 B：`Minikube` 全量部署（中间件 + 业务服务都在 K8s 内）

---

## 一、路径 A：Docker 中间件 + 本地服务

首次启动：

```powershell
# 0) 准备环境变量（cmd/all 与单服务都读这些值）
Copy-Item .\deployments\docker-compose\.env.example .\deployments\docker-compose\.env -Force

# 1) 启动中间件（PostgreSQL/Redis/Kafka/MinIO/ES/**etcd**）
docker compose -f .\deployments\docker-compose\docker-compose.infra.yml up -d

# 2) 启动本地全部服务
go run .\cmd\all
```

日常启动：

```powershell
docker compose -f .\deployments\docker-compose\docker-compose.infra.yml start
go run .\cmd\all
```

关闭：

```powershell
docker compose -f .\deployments\docker-compose\docker-compose.infra.yml stop
```

### 验证

- 确认 **`gateway-lb`** 已随 infra compose 启动（宿主 **`http://localhost:28080`** → Nginx → 两台 gateway）。
- 打开 `web/pages/login.html`（`file://` 时 API 默认指向 **`http://localhost:28080`**）。
- 登录成功，收发消息正常。

### 注意点

- `cmd/all` 会自动读取 `deployments/docker-compose/.env`，所有端口与路由都由 env 控制。
- **etcd** 默认 `ETCD_ENDPOINTS=127.0.0.1:2379`（与 infra compose 中 `2379:2379` 一致）；各服务 gRPC 发现仅走 etcd，不再使用 `*_SERVICE_GRPC_TARGET`。
- 浏览器应走 **Nginx 入口 `28080`**，由上游 `least_conn` 分到 **26080 / 26180** 两台 gateway；直连单台网关仅排障时使用。

---

## 二、路径 B：Minikube 全量部署（推荐联调）

把整套服务部署到 `pim` 命名空间，并通过固定 `port-forward` 使用稳定入口。

### 2.1 首次部署

```powershell
# 1) 启动 minikube
minikube start --driver=docker

# 2) 构建镜像到 minikube 节点 Docker
minikube -p minikube docker-env | Invoke-Expression
.\deployments\k8s\scripts\build-images.ps1

# 3) 部署资源
kubectl apply -f .\deployments\k8s\base\
kubectl apply -f .\deployments\k8s\infra\
kubectl wait --for=condition=ready pod -l app=kafka -n pim --timeout=300s
kubectl apply -f .\deployments\k8s\infra\kafka-init-job.yaml
kubectl apply -f .\deployments\k8s\infra\minio-init-job.yaml
kubectl apply -f .\deployments\k8s\apps\

# 4) 固定端口转发
.\deployments\k8s\scripts\start-port-forward.ps1

# 5) 停止端口转发
.\deployments\k8s\scripts\stop-port-forward.ps1
```

### 2.2 日常重启

```powershell
minikube start --driver=docker
kubectl apply -f .\deployments\k8s\base\
kubectl apply -f .\deployments\k8s\infra\
kubectl apply -f .\deployments\k8s\apps\
.\deployments\k8s\scripts\start-port-forward.ps1

# 停止端口转发
.\deployments\k8s\scripts\stop-port-forward.ps1
```

### 这块怎么验证

- `kubectl get pods -n pim` 业务 Pod 全部 `Running`。
- 网关统一入口：`http://127.0.0.1:26080`。
- 浏览器访问 `http://127.0.0.1:8088/pages/login.html`。
- 登录和收发消息正常。

---
