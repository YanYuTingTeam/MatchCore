# MatchCore

独立于 Minecraft 服务端的 Go 匹配服务框架, 本轮提供配置加载、Redis 连接池、HTTP 健康检查和业务接口占位

完整职责、兼容协议和迁移步骤见 [改动方案](PLAN.md), 本轮进度与验证见 [TASK.md](TASK.md)

## 当前可用内容

- Go 1.26 或更新版本, 本机验证环境为 Go 1.27.2
- 标准库 HTTP 服务, 有界读取和写入超时, 信号触发优雅关闭
- go-redis v9.23.0 单连接池, 支持 Redis ACL 用户、密码和 TLS
- `/healthz` 返回进程存活, `/readyz` 通过真实 Redis PING 检查连接
- 业务接口使用 `Authorization: Bearer <token>`
- 子服查询只有 `serverquery.Client` 类型接口, 未实现 SLP 或后台调度

当前没有房间扫描、快照读写、匹配算法或入房发布. 业务接口返回 501, 不能作为完整匹配后端上线

## 启动

在 `D:/Github/MatchCore` 执行

```powershell
Copy-Item -LiteralPath '.\.env.example' -Destination '.\.env'
$env:MATCHCORE_API_TOKEN = [Convert]::ToHexString([System.Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
go run ./cmd/matchcore
```

上面的令牌仅保存在当前进程环境, 不输出或写入配置. 后续插件接入时由运维使用同一令牌, 生产环境通过服务管理器或密钥管理传入稳定值

服务默认读取 `.env`, 也可用 `-config .env.local` 指定文件. 所有参数均可写入 `.env`, 同名进程环境变量优先, 包括显式空值. 未设置的参数使用表内默认值, 令牌必须由文件或进程环境提供

Redis 有密码时设置 `MATCHCORE_REDIS_PASSWORD`. 包含 `$`、`#`、`=` 或双引号的密码应使用单引号, 如 `MATCHCORE_REDIS_PASSWORD='pass=word#value$part"end'`. dotenv 的未加引号或双引号值会展开变量, 单引号保留字面值. `.env` 和本地变体已忽略, 不提交真实密钥

默认仅监听 `127.0.0.1:8080`. 跨主机接入时绑定受控私网地址并限制可信子服访问, 跨不可信网络通过 HTTPS 反向代理接入. Redis 同样仅对可信服务开放

Redis 暂时不可达时进程仍启动, `/healthz` 返回 200, `/readyz` 返回 503. 连接恢复后 `/readyz` 可重新返回 200, 不需要重启. 当前就绪只表示基础 Redis 连接可用, 不表示匹配能力已实现

## HTTP 接口

| 方法 | 路径 | 本轮行为 |
| --- | --- | --- |
| GET | `/healthz` | 200, `{"code":"ok"}`, 不访问 Redis |
| GET | `/readyz` | Redis PING 成功为 200, 失败为 503 `redis_unavailable` |
| GET | `/v1/groups` | 有效鉴权后 501 `not_implemented` |
| GET | `/v1/groups/{group}/servers` | 有效鉴权后 501 `not_implemented` |
| POST | `/v1/matches` | 有效鉴权后 501 `not_implemented` |

业务接口无鉴权或重复鉴权头返回 401. 健康检查仅供运维, 玩家数据读取不调用 `/readyz`, 避免额外 Redis PING

## 配置

| 字段 | 默认值 | 约束 |
| --- | --- | --- |
| `MATCHCORE_HTTP_ADDR` | `127.0.0.1:8080` | `host:port`, 端口 1 到 65535 |
| `MATCHCORE_REDIS_ADDR` | `127.0.0.1:6379` | 非空主机与端口, 支持 `[::1]:6379` |
| `MATCHCORE_REDIS_USERNAME` | 空 | Redis ACL 用户名 |
| `MATCHCORE_REDIS_DB` | 0 | 不得小于 0, 实际上限由 Redis 决定 |
| `MATCHCORE_REDIS_TLS` | false | 布尔值, 开启时校验证书与主机名, 最低 TLS 1.2 |
| `MATCHCORE_REDIS_POOL_SIZE` | 10 | 1 到 128, 同时作为活跃连接上限 |
| `MATCHCORE_REDIS_TIMEOUT` | `2s` | 100ms 到 5s, 用于连接、读写、取连接和就绪检查 |
| `MATCHCORE_SHUTDOWN_TIMEOUT` | `10s` | 1s 到 1m |
| `MATCHCORE_API_TOKEN` | 必填 | 32 到 256 字节的可见 ASCII, 推荐随机十六进制 |
| `MATCHCORE_REDIS_PASSWORD` | 空 | Redis 密码, 不记日志 |

配置使用 dotenv 键值格式, 支持引号、注释和 `export`, 文件最大 64 KiB. 文件缺失、格式错误、未知配置键、无效超时和地址会阻止启动. `.env.local` 等文件仅在明确指定时加载, 不自动叠加. 当前使用单节点 Redis, Sentinel/Cluster 是后续部署需求

## 开发检查

```powershell
go test ./...
go vet ./...
go build ./...
```

本轮不创建提交. 既有 `.gitignore` 忽略 `TASK.md`, 将来如果需要版本化任务记录, 由用户决定是否调整该忽略规则
