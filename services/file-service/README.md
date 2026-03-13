## File Service（文件服务）

**职责**：

- 聊天图片、文件的上传与下载
- 文件元数据管理（所有者、大小、类型、URL 等）
- 文件访问权限控制（结合用户身份）
- 对接对象存储（MinIO / 云 OSS）
- 异步安全检测（鉴黄、病毒扫描等，可选）

计划使用的组件：

- Go + Gin
- PostgreSQL：`files` 元数据表
- MinIO：对象存储（本地开发环境）
- Kafka：安全检测相关的异步任务（可选）

建议目录结构（后续与你一起补充具体代码）：

```text
file-service/
  cmd/
    file-service/     # main.go（HTTP 入口）
  internal/
    handler/          # HTTP handler
    service/          # 文件相关业务逻辑
    repo/             # Postgres 访问
    storage/          # MinIO / OSS 访问封装
    config/           # 配置加载
```

