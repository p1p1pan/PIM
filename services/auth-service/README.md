## Auth Service（认证服务）

**职责**：

- 用户登录认证（校验用户名密码）
- JWT 访问令牌 & 刷新令牌的签发与刷新
- Token 校验（供网关和其它服务调用）
- 基于角色的访问控制（RBAC）

计划使用的组件：

- Go + gRPC
- PostgreSQL：存储用户凭据、角色和权限
- Redis：Token 黑名单、角色权限缓存

建议目录结构（后续与你一起补充具体代码）：

```text
auth-service/
  cmd/
    auth-service/     # main.go（gRPC/HTTP 入口）
  internal/
    handler/          # gRPC / HTTP handler
    service/          # 认证与权限业务逻辑
    repo/             # Postgres / Redis 访问
    config/           # 配置加载
    middleware/       # 认证相关中间件（如拦截器）
  proto/              # 认证服务 gRPC 协议定义
```

