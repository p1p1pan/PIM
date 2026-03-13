## services 目录说明

本目录用于存放所有微服务的代码，每个子目录对应一个独立服务，推荐结构如下（后续与你一起逐步填充 Go 代码）：

- `gateway/`：网关服务（HTTP/WebSocket 统一入口、路由、限流、鉴权）
- `auth-service/`：认证服务（登录、JWT 签发与校验、RBAC）
- `user-service/`：用户服务（注册、资料管理、搜索）
- `friend-service/`：好友关系服务（申请、同意/拒绝、拉黑、好友列表）
- `conversation-service/`：对话服务（单聊、消息存储、未读、撤回）
- `group-service/`：群聊服务（群管理、成员管理、群消息）
- `file-service/`：文件服务（上传下载、元数据管理、MinIO 对接）
- `log-service/`：日志服务（日志聚合与检索接口）

后续每个服务内部推荐目录结构（示例）：

```text
<service-name>/
  cmd/
    <service-name>/        # main.go 入口（后面一起补）
  internal/
    handler/               # HTTP / gRPC handler
    service/               # 业务逻辑
    repo/                  # 数据访问（PostgreSQL / Redis 等）
    config/                # 配置加载
    middleware/            # 中间件（如鉴权、日志）
  proto/                   # 该服务对外暴露的 gRPC 协议文件
```

当前阶段仅创建目录与占位文件，不写具体实现代码，方便后续循序渐进地一起补充。

