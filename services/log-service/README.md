## Log Service（日志服务）

**职责**：

- 统一接收各服务的结构化日志（通常来自 Kafka）
- 将日志写入 Elasticsearch，提供检索能力
- 为前端（如管理后台）提供简单的日志查询接口（可选）

计划使用的组件：

- Go + Gin / gRPC（按需要）
- Kafka：日志 Topic（如 `log-topic`）
- Elasticsearch：日志索引（如 `logs_index`）

建议目录结构（后续与你一起补充具体代码）：

```text
log-service/
  cmd/
    log-service/      # main.go（消费 Kafka + 对外检索接口）
  internal/
    handler/          # HTTP / gRPC 日志查询接口（可选）
    service/          # 日志聚合与查询业务逻辑
    repo/             # Elasticsearch 访问封装
    mq/               # Kafka 消费逻辑
    config/           # 配置加载
```

