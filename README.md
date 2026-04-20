# PIM

一个基于 Go 的 IM（即时通讯）练手项目，包含用户、认证、好友、会话、群聊、文件、日志与网关等服务，并提供 Docker Compose 与 Minikube 两种部署方式。

## 项目定位

PIM 的目标不是做一个“单页聊天室 Demo”，而是把 IM 系统里常见的核心能力串成一条完整可运行链路：鉴权、关系链、消息链路、推送链路、文件与观测。  
项目重点放在“服务之间如何协作”与“链路在真实中间件上的流转”，适合用来练习微服务拆分和联调。

## 核心业务链路

### 1) 账号与鉴权链路（注册 -> 登录 -> 持续鉴权）

- 用户在 `web/pages/register.html` 提交用户名和密码，请求经 `gateway` 转发到 `auth-service`
- 注册成功后，用户在 `web/pages/login.html` 登录，`auth-service` 签发 JWT
- 前端保存会话信息，后续请求统一带 token；网关鉴权中间件先校验，再分发到具体业务服务
- 无效或过期 token 会被网关拦截，前端回到登录流程

### 2) 关系链路（好友请求 -> 通过 -> 会话准备）

- 登录用户发起好友申请，请求经网关进入关系相关服务（`friend-service` / `user-service`）
- 对方在好友请求列表中处理申请（通过/拒绝），状态变更写入存储
- 当关系建立后，消息链路才进入“可投递”状态，减少无效投递
- 前端通过定时刷新与推送事件补齐状态，保证会话列表和关系状态一致

### 3) 单聊消息链路（发送 -> Kafka -> 消费 -> WebSocket 推送）

- 发送方在 `web/pages/im.html` 输入消息，经网关进入消息入口（`conversation-service`）
- 消息先写入消息总线（Kafka），把“写入请求”和“后续处理”解耦
- 消费侧按会话/用户维度处理消息（落库、构建推送事件、更新会话状态）
- 网关通过 WebSocket 把消息推送给在线接收方；离线用户可在后续拉取中补齐
- 前端收到推送后更新会话窗口，并做必要的本地状态修正（如本地回显转正式消息）

### 4) 群聊链路（建群/入群 -> 群消息投递 -> 成员一致性）

- 群管理操作（建群、加人、成员变更）经网关进入 `group-service`
- 群消息走独立的 Kafka topic 与消费逻辑，避免与单聊链路互相干扰
- 消费端根据群成员关系分发推送，在线成员实时收到消息
- 成员变化会触发同步事件，降低“群成员视图”与“投递目标”不一致的概率

### 5) 文件与可观测链路（上传 -> 扫描 -> 观测/告警）

- 文件上传由 `file-service` 处理，对象存储落在 MinIO
- 扫描任务通过 Kafka 进行异步处理，异常可进入 DLQ 等待排查与补偿
- 网关与各服务暴露指标，Prometheus 采集，Grafana 展示
- 项目内置了告警规则和演练脚本，用于验证异常场景下的告警行为

## 项目特点

- 多服务拆分：`auth`、`user`、`friend`、`conversation`、`group`、`file`、`log`、`gateway`
- 通信方式：HTTP + gRPC + WebSocket + Kafka
- 数据与中间件：PostgreSQL、Redis、Kafka、MinIO、Elasticsearch
- 观测能力：Prometheus + Grafana（配套告警规则与演练脚本）
- 前端为轻量静态页面，便于本地快速联调

## 目录结构

```text
.
├─ cmd/                 # 各服务启动入口（含 cmd/all 一键启动）
├─ internal/            # 业务实现
├─ pkg/                 # 公共包
├─ proto/               # gRPC 协议定义
├─ web/                 # 前端静态页面与脚本
├─ deployments/         # Docker Compose / K8s / observability 配置
├─ test/                # 测试
└─ doc/                 # 补充文档
```

## 环境要求

- Go `1.25+`
- Docker / Docker Compose
- （可选）Minikube + kubectl，用于 K8s 全量部署

## 快速开始

### 方式 A：Docker 中间件 + 本地服务（推荐）

```powershell
# 1) 准备环境变量
Copy-Item .\deployments\docker-compose\.env.example .\deployments\docker-compose\.env -Force

# 2) 启动中间件
docker compose -f .\deployments\docker-compose\docker-compose.infra.yml up -d

# 3) 启动全部业务服务
go run .\cmd\all
```

访问前端：

- 本地静态文件：`web/index.html`
- 浏览器 API 入口：默认经 Nginx 负载均衡 **`http://localhost:28080`**（`gateway-lb` → 26080/26180）；需与 `go run ./cmd/all`、infra compose 一并启动。

### 方式 B：Minikube 全量部署

完整命令与说明见：`deployments/README.md`

常用最短流程：

```powershell
minikube start --driver=docker
minikube -p minikube docker-env | Invoke-Expression
.\deployments\k8s\scripts\build-images.ps1
kubectl apply -f .\deployments\k8s\base\
kubectl apply -f .\deployments\k8s\infra\
kubectl apply -f .\deployments\k8s\apps\
.\deployments\k8s\scripts\start-port-forward.ps1
```

## 前端说明

- 前端入口说明：`web/README.md`
- 统一入口页：`web/index.html`
- 业务页面入口：`web/pages/login.html`、`web/pages/register.html`、`web/pages/im.html`、`web/pages/admin.html`

## 相关文档

- 部署文档：`deployments/README.md`
- 前端文档：`web/README.md`

## 说明

该项目主要用于学习和联调实践，接口与页面会随阶段推进持续调整。  
如果你正在跟进某一阶段任务，优先以 `deployments/README.md` 和当前分支代码为准。
