# MatchCore

独立于 Minecraft 服务端的 Go 匹配服务, 集中采集 SLP/Redis 状态、维护分组、返回优选房间并写入60秒入房票据

完整职责、兼容协议和迁移步骤见 [改动方案](PLAN.md), 本轮进度与验证见 [TASK.md](TASK.md)

## 当前可用内容

- Go 1.26 或更新版本, 本机验证环境为 Go 1.27.2
- 标准库 HTTP 服务, 有界读取和写入超时, 信号触发优雅关闭
- go-redis v9.23.0 单连接池, 支持 Redis ACL 用户、密码和 TLS
- `/healthz` 返回进程存活, `/readyz` 通过真实 Redis PING 检查连接
- 业务接口使用 `Authorization: Bearer <token>`
- 基于协议47的真实TCP SLP, 只查询groups.yml中声明的服务器, 最大8个并发
- Redis完整SCAN去重、管道HMGET/PTTL、同版本分组定义与房间快照, 有时间/键数/返回大小预算
- 玩法分组和过滤规则集中维护, 同名房间按完整来源key区分
- POST选房返回前先成功SET票据, 状态/余位过滤、地图优先、人数最多优先、并列随机

Go不发布原入房消息或操作Minecraft玩家. AerJoiner core保留PUBLISH/Connect, MatchHandler本阶段仅打印PlayerJoin票据校验状态和目标, 不消费票据或加入游戏. 本轮没有席位预留或真实游戏加入确认

## 启动

在 `D:/Github/MatchCore` 执行

```powershell
if (-not (Test-Path -LiteralPath '.\.env')) { Copy-Item -LiteralPath '.\.env.example' -Destination '.\.env' }
$env:MATCHCORE_API_TOKEN = [Convert]::ToHexString([System.Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
go run ./cmd/matchcore
```

上面的令牌仅保存在当前进程环境, 不输出或写入配置. 后续插件接入时由运维使用同一令牌, 生产环境通过服务管理器或密钥管理传入稳定值

服务默认读取 `.env`, 也可用 `-config .env.local` 指定文件. 所有参数均可写入 `.env`, 同名进程环境变量优先, 包括显式空值. 未设置的参数使用表内默认值, 令牌必须由文件或进程环境提供

Redis 有密码时设置 `MATCHCORE_REDIS_PASSWORD`. 包含 `$`、`#`、`=` 或双引号的密码应使用单引号, 如 `MATCHCORE_REDIS_PASSWORD='pass=word#value$part"end'`. dotenv 的未加引号或双引号值会展开变量, 单引号保留字面值. `.env` 和本地变体已忽略, 不提交真实密钥

默认仅监听 `127.0.0.1:8080`. 跨主机接入时绑定受控私网地址并限制可信子服访问, 跨不可信网络通过 HTTPS 反向代理接入. Redis 同样仅对可信服务开放

Redis 暂时不可达时进程仍启动, `/healthz` 返回 200, `/readyz` 返回 503. 连接恢复后 `/readyz` 可重新返回 200, 不需要重启. 就绪表示基础Redis连接可用, 不表示当前分组必有可加入房间或游戏已实际加入成功

启动前编辑groups.yml的玩法和真实Bungee服务名/地址. 默认示例包含solo Redis组与本地SLP示例, 不代表真实线上配置. 首次采集成功前业务返回503; 修改分组后重启, 后端生成统一configVersion并在HTTP/Redis中下发. 相对groups文件路径基于指定.env所在目录

## HTTP 接口

| 方法 | 路径 | 本轮行为 |
| --- | --- | --- |
| GET | `/healthz` | 200, `{"code":"ok"}`, 不访问 Redis |
| GET | `/readyz` | Redis PING 成功为 200, 失败为 503 `redis_unavailable` |
| GET | `/v1/groups` | 全部分组定义与rooms的Snapshot |
| GET | `/v1/groups/{group}/servers` | 单组同版本Snapshot, 未知组404 |
| POST | `/v1/matches` | playerUuid/group/arenaName可选, 成功Ticket, 无房409, 数据或写入失败503 |

业务接口无鉴权或重复鉴权头返回 401. 健康检查仅供运维, 玩家数据读取不调用 `/readyz`, 避免额外 Redis PING

POST只接受16KiB以内application/json, 参数/UUID错误400, 未知组404. 所有时间为Unix毫秒. 完整字段、Redis键、过期与降级规则见 [PROTOCOL.md](PROTOCOL.md). fixtures/config.json是Redis分组定义样例, 不是服务配置文件; snapshot.json和ticket.json为真实Go模型序列化, 仅用于跨语言测试, 假token不可用于生产

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
| `MATCHCORE_GROUPS_FILE` | `groups.yml` | 独立分组文件, 最大1MiB, 相对.env目录 |
| `MATCHCORE_REDIS_PREFIX` | `matchcore:` | 独立namespace, 不可与原始房间prefix重叠 |
| `MATCHCORE_REFRESH_INTERVAL` | `3s` | 每轮结束后的间隔, 1s到1m |
| `MATCHCORE_COLLECT_TIMEOUT` | `5s` | 整轮采集时限, 100ms到30s |
| `MATCHCORE_SNAPSHOT_TTL` | `15s` | 不小于间隔+采集时限, 不超过5m |

配置使用 dotenv 键值格式, 支持引号、注释和 `export`, 文件最大 64 KiB. 文件缺失、格式错误、未知配置键、无效超时和地址会阻止启动. `.env.local` 等文件仅在明确指定时加载, 不自动叠加. 当前使用单节点 Redis, Sentinel/Cluster 是后续部署需求

票据TTL固定60秒, 与服务鉴权Token无关. 原始bedwars Hash的updatedAt只表示状态更新时间, 在线判断依据PTTL. 单台SLP离线为正常OFFLINE, Redis错误/扫描不完整则不发布新快照. MOTD没有原始Hash降级, 快照过期后不能使用陈旧目标

## 开发检查

```powershell
go test ./...
go vet ./...
go build ./...
```

测试使用受控miniredis和本地TCP/HTTP, 覆盖跨服同名地图、房间过期/满员/状态、Redis写入失败、扫描预算和SLP分片/取消. 没有访问生产Redis或真实Minecraft客户端, 实服与跨插件组合联调仍需要验证

本轮不创建提交. 既有 `.gitignore` 忽略 `TASK.md`, 将来如果需要版本化任务记录, 由用户决定是否调整该忽略规则
