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

