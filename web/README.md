# Web 目录说明

为避免入口分散，统一从 `index.html` 进入，业务页面入口放在 `pages/` 目录。

## 页面入口

- `index.html`: 统一入口首页（登录、后台）
- `pages/login.html`: 登录页
- `pages/register.html`: 注册页
- `pages/im.html`: IM 主界面（好友/会话/群聊）
- `pages/admin.html`: 管理后台（观测、日志查询、健康、DLQ，监督页）

说明：

- **Docker 中启动 `gateway-lb` 后**，浏览器可访问 **`http://localhost:28080/`**（与 `/api`、`/ws` 同源，见 `deployments/docker/nginx.gateway-lb.conf`）；亦可本地直接打开 `index.html`（`file://`）。
- 用户主链路：`index -> pages/login -> pages/im`
- 后台监督链路：`index -> pages/admin`
- `pages/login` 可返回 `index`

## 目录分层

- `auth/`: 登录注册脚本与样式
- `im/`: IM 页面脚本与样式
- `admin/`: 管理后台脚本与样式
- `common/`: 公共启动与请求方法
- `pages/`: 页面入口 HTML（登录、注册、IM、后台）
