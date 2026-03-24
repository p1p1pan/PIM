# Docker Compose 本地基础设施

## 阶段 0：PostgreSQL + Redis

### 启动

在**本目录**下执行：

```bash
docker compose -f docker-compose.infra.yml up -d
```

查看日志确认无报错：

```bash
docker compose -f docker-compose.infra.yml logs -f
```

### 连接信息

| 组件       | 地址            | 端口 | 账号/密码 |
|------------|-----------------|------|-----------|
| PostgreSQL | localhost       | 5432 | 用户 `pim`，密码 `pim`，库 `pim` |
| Redis      | localhost       | 6379 | 无密码    |

- **PostgreSQL 连接串**（Go / DBeaver 等）：  
  `postgres://pim:pim@localhost:5432/pim?sslmode=disable`
- **Redis**：`localhost:6379`（无密码）

### 各 Go 服务的数据库连接池（可选环境变量）

使用 PostgreSQL 的微服务通过 `internal/db.OpenPostgres()` 统一设置 `database/sql` 连接池。多进程连同一库时，各进程 `POSTGRES_MAX_OPEN_CONNS` 之和应小于 PostgreSQL 的 `max_connections`（默认常见为 100）。

| 环境变量 | 默认值 | 含义 |
|----------|--------|------|
| `POSTGRES_MAX_OPEN_CONNS` | `15` | 单进程最大打开连接数 |
| `POSTGRES_MAX_IDLE_CONNS` | `8` | 连接池中最大空闲连接（不会超过 MaxOpen） |
| `POSTGRES_CONN_MAX_LIFETIME_SEC` | `300` | 连接最大存活时间（秒），`0` 表示不限制 |
| `POSTGRES_CONN_MAX_IDLE_TIME_SEC` | `90` | 空闲连接最大保留时间（秒），`0` 表示不限制 |

各使用 Kafka 的 Go 服务进程还可设置（**重启进程生效**）：

| 环境变量 | 默认值 | 含义 |
|----------|--------|------|
| `KAFKA_PRODUCER_LOW_LATENCY` | `true` | `true`：leader ack、无压缩、尽快 flush（低延迟）；`false`：`WaitForAll` 全副本确认（更慢、更稳） |
| `KAFKA_PRODUCER_DEBUG_LOG` | `false` | `true` 时每条成功写入 Kafka 打日志（高 QPS 时严重拖慢，仅排查） |
| `KAFKA_CONVERSATION_IM_GROUP_ID` | `conversation-im-message` | conversation-service 消费 `im-message` 的 consumer group ID（多实例扩展时需保持同组） |
| `KAFKA_CONVERSATION_READ_GROUP_ID` | `conversation-im-message-read` | conversation-service 消费 `im-message-read` 的 consumer group ID |
| `KAFKA_CONVERSATION_PUSH_GROUP_ID` | `conversation-im-message-push` | conversation-service 消费 `im-message-push`（存推分离后的推送 topic）的 consumer group ID |
| `KAFKA_CONVERSATION_IM_BATCH_SIZE` | `64` | `im-message` 每分区批处理条数上限（分区内串行） |
| `KAFKA_CONVERSATION_IM_BATCH_WAIT_MS` | `5` | `im-message` 每分区批处理最大等待时间（毫秒） |
| `KAFKA_CONVERSATION_PUSH_BATCH_SIZE` | `64` | `im-message-push` 每分区批处理条数上限 |
| `KAFKA_CONVERSATION_PUSH_BATCH_WAIT_MS` | `3` | `im-message-push` 批处理最大等待时间（毫秒） |
| `KAFKA_CONVERSATION_PUSH_ROUTE_CACHE_TTL_MS` | `1500` | push 路由本地缓存 TTL（毫秒）；减少热用户重复查询 Redis，`0` 表示关闭 |
| `KAFKA_CONVERSATION_CONSUMER_VERBOSE_LOG` | `false` | `true` 时每条成功消费打 stdout + `log-topic`（高 QPS 时显著拖慢，仅排障） |
| `KAFKA_CONSUMER_CHANNEL_BUFFER_SIZE` | `1024` | Sarama consumer channel 缓冲（过小易反压） |
| `KAFKA_CONSUMER_FETCH_MAX_BYTES` | `1048576` | 单次 fetch 最大字节数 |
| `KAFKA_CONSUMER_MAX_WAIT_TIME_MS` | `100` | broker 端聚合 fetch 的最大等待（毫秒） |
| `KAFKA_CONSUMER_COMMIT_EVERY_BATCH` | `4` | 每 N 个成功批次提交一次 offset（降低提交开销；N 越大崩溃时重复窗口越大） |
| `GATEWAY_PUSH_BATCH_PARALLEL` | `32` | gateway 处理 `PushBatchToConn` 时按接收者分链并行度（同用户仍串行） |
| `CONVERSATION_HTTP_ADDR` | `:9003` | conversation-service HTTP（health/metrics）监听地址；多实例时需改端口避免冲突 |
| `CONVERSATION_GRPC_ADDR` | `:9013` | conversation-service gRPC 监听地址；多实例时需改端口避免冲突 |
| `GROUP_HTTP_ADDR` | `:9004` | group-service HTTP（health/metrics）监听地址；多实例时需改端口避免冲突 |
| `GROUP_GRPC_ADDR` | `:9014` | group-service gRPC 监听地址；多实例时需改端口避免冲突 |
| `GROUP_SERVICE_GRPC_TARGET` | `localhost:9014` | gateway/file-service 调用 group-service 的目标地址 |
| `GATEWAY_PUSH_GRPC_TARGET` | `localhost:8090` | group-service 调用 gateway PushService 的目标地址 |

`docker-compose.infra.yml` 中的 **`kafka-init`** 会在 Kafka 就绪后创建多分区 topic（如 `im-message` / `im-message-push` / `group-message` 为 **64** 分区）。若本地曾用自动创建得到 **1 分区** 或较低分区数 topic，需要先删除对应 topic 再 `compose up` 让 `kafka-init` 重建，否则消费并行度仍受限。

**Kafka 监听说明（避免 kafka-init / 容器内工具连不上）：**  
Broker 使用 **双 listener**：宿主机上的 Go 服务仍连 **`localhost:9092`**（`KAFKA_BROKERS` 不变）；Docker 网络内的容器（含 `kafka-init`）使用 **`kafka:29092`**。若只把 `advertised` 设为 `localhost`，容器内客户端会先连 `kafka:9092`，但元数据返回 `localhost`，在容器内会指向自身而非 broker，从而出现 `Connection to node 1 (localhost/127.0.0.1:9092) could not be established`。

### 验证是否正常

**PostgreSQL**

```bash
docker exec -it pim-postgres psql -U pim -d pim -c "SELECT 1;"
```

**Redis**

```bash
docker exec -it pim-redis redis-cli ping
```

应返回 `PONG`。

### 停止

```bash
docker compose -f docker-compose.infra.yml down
```

数据在 named volume 中会保留；若需清空数据可加 `-v`：

```bash
docker compose -f docker-compose.infra.yml down -v
```

