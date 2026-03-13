## Friend Service（好友关系服务）

**职责**：

- 好友申请发起、同意/拒绝
- 删除好友、拉黑/取消拉黑
- 好友列表获取
- 为其它服务提供“是否好友”的批量校验能力

计划使用的组件：

- Go + Gin + gRPC
- PostgreSQL：好友关系、申请记录、黑名单等表
- Redis：好友列表缓存

建议目录结构（后续与你一起补充具体代码）：

```text
friend-service/
  cmd/
    friend-service/   # main.go（HTTP/gRPC 入口）
  internal/
    handler/          # HTTP / gRPC handler
    service/          # 好友关系业务逻辑
    repo/             # Postgres / Redis 访问
    config/           # 配置加载
    middleware/       # 与好友接口相关的中间件
  proto/              # 好友服务 gRPC 协议定义
```

