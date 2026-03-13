## Gateway Service（网关服务）

**职责**：作为系统统一入口，负责：

- 接收客户端 HTTP / WebSocket 请求
- 基于 JWT 进行认证与简单鉴权
- 将请求路由到后端各业务服务（User/Friend/Conversation/Group/File 等）
- 实现基础的限流和熔断保护

后续建议的目录结构（暂不创建 Go 代码，仅作规划）：

```text
gateway/
  cmd/
    gateway/          # main.go（服务入口）
  internal/
    handler/          # HTTP / WebSocket 处理
    middleware/       # 鉴权、日志、限流中间件
    router/           # 路由注册
    config/           # 配置加载
```

后面会在你的参与下，一步一步在这些目录中补充实际的 Go 代码。

