## Group Service（群聊服务）

**职责**：

- 群组创建/解散
- 群成员管理（邀请、移除、角色：owner/admin/member）
- 群禁言/解禁
- 群消息发送与扇出（与消息队列和 WebSocket 网关协作）
- 群搜索（接入 Elasticsearch）

计划使用的组件：

- Go + Gin + gRPC
- PostgreSQL：`groups`、`group_members`、`group_messages` 等表
- Redis：群成员列表缓存、禁言状态
- Kafka：群消息扇出
- Elasticsearch：群搜索索引

建议目录结构（后续与你一起补充具体代码）：

```text
group-service/
  cmd/
    group-service/    # main.go（HTTP/gRPC 入口）
  internal/
    handler/          # HTTP / gRPC handler
    service/          # 群聊相关业务逻辑
    repo/             # Postgres / Redis 访问
    mq/               # 群消息队列封装
    config/           # 配置加载
  proto/              # 群聊服务 gRPC 协议定义
```

