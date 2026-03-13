## K8s 部署说明（占位）

本目录用于存放各个服务以及基础组件的 Kubernetes 配置，目标是：

- 在 kind / minikube 上搭建本地 K8s 集群
- 为每个服务创建对应的 `Deployment` 与 `Service`
- 通过 Ingress 或 Gateway 对外暴露网关服务

后续计划的子目录（暂不编写具体 YAML，先规划结构）：

- `infra/`
  - PostgreSQL、Redis、MinIO、Kafka、Elasticsearch、Prometheus、Grafana、Kibana 等
- `services/`
  - `gateway/`
  - `auth-service/`
  - `user-service/`
  - `friend-service/`
  - `conversation-service/`
  - `group-service/`
  - `file-service/`
  - `log-service/`
- `ingress/`
  - 网关 Ingress 配置（如 `pim.local` 域名）

等你对 Docker Compose 比较熟悉之后，我们可以一步步把其中的组件迁移到 K8s YAML 中。

