# Web 目录说明

为避免入口分散，统一从 `index.html` 进入。

## 页面入口

- `index.html`: 统一入口首页（登录、后台）
- `login.html`: 登录页
- `register.html`: 注册页
- `im.html`: IM 主界面（好友/会话/群聊）
- `admin.html`: 管理后台（观测、日志查询、健康、DLQ，监督页）

说明：

- 用户主链路：`index -> login -> im`
- 后台监督链路：`index -> admin`
- `login` 可返回 `index`

## 目录分层

- `auth/`: 登录注册脚本与样式
- `im/`: IM 页面脚本与样式
- `admin/`: 管理后台脚本与样式
- `common/`: 公共启动与请求方法
