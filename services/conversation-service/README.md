## Conversation Service（对话 / 消息服务）

**职责**：

- 单聊会话管理（会话列表、最近一条消息）
- 消息存储（文本、表情等）
- 未读计数管理
- 消息撤回、已读回执
- 与 WebSocket 网关协作，实现消息的实时投递

计划使用的组件：

- Go + Gin + gRPC
- WebSocket（通过网关或独立 WS 服务）
- PostgreSQL：`conversations`、`messages`、`message_reads` 等表
- Redis：未读计数、用户 WebSocket 路由
- Kafka/RabbitMQ：单聊消息的异步持久化与推送事件

建议目录结构（后续与你一起补充具体代码）：

```text
conversation-service/
  cmd/
    conversation-service/  # main.go（HTTP/gRPC 入口）
  internal/
    handler/               # HTTP / gRPC handler
    service/               # 对话与消息业务逻辑
    repo/                  # Postgres / Redis 访问
    mq/                    # 消息队列 producer/consumer 封装
    config/                # 配置加载
  proto/                   # 对话服务 gRPC 协议定义
```

