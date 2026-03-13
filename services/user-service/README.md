## User Service（用户服务）

**职责**：

- 用户注册、基础信息管理
- 用户登录入口（调用认证服务获取 JWT）
- 用户资料查询与更新
- 用户搜索（接入 Elasticsearch）
- 用户在线状态查询（基于 Redis）

计划使用的组件：

- Go + Gin + gRPC
- PostgreSQL：`users` 等用户相关表
- Redis：用户资料/在线状态缓存
- Elasticsearch：`users_index` 用户搜索索引

建议目录结构（后续与你一起补充具体代码）：

```text
user-service/
  cmd/
    user-service/     # main.go（HTTP/gRPC 入口）
  internal/
    handler/          # HTTP / gRPC handler
    service/          # 用户领域业务逻辑
    repo/             # Postgres / Redis 访问
    config/           # 配置加载
    middleware/       # 与用户相关的中间件（如用户鉴权）
  proto/              # 用户服务 gRPC 协议定义
```

