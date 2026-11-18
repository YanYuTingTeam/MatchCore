# MatchCore 共享协议 v1

本文件是 MatchCore、AerJoiner core 与 MatchHandler 的共同字段依据, 所有时间为 Unix 毫秒, JSON 字段使用 lowerCamelCase, schemaVersion 固定为 1

## 职责和密钥

- MatchCore 采集、分组、选房, 返回前先成功写 Redis 票据, 不 PUBLISH
- core 优先 HTTP, 故障时读取 Redis 快照或受控原始 Hash, 按同协议写票据, Redis 房间状态只读, 原 bedwars:match 发布与 Connect 由 core 保留
- Handler 按 PlayerJoin 的玩家 UUID 读取票据, 校验状态与目标, 只记录校验状态和目标, 不消费票据、不输出 token、不调用游戏 API
- 服务鉴权 MATCHCORE_API_TOKEN 与玩家票据 token 相互独立. 玩家 token 使用 32 字节安全随机数的小写十六进制, 长度 64

## Redis 键

prefix 默认 matchcore:, 两端配置必须一致

| 键 | 值 | 有效期 |
| --- | --- | --- |
| `<prefix>config` | Definition JSON | 持久, 分组启动配置 |
| `<prefix>snapshot` | Snapshot JSON | 默认 15 秒, 同时校验 expiresAt |
| `<prefix>ticket:<标准小写UUID>` | Ticket JSON | 60 秒, 同时校验 expiresAt |

票据使用 SET 带 60 秒 TTL. 本轮不做容量预留或消费, 同一玩家新票据覆盖旧票据. core 同一玩家请求锁阻止并发点击, 不把快照读取失败重试变成重复转服

## 结构

Room 全部字段:

```json
{"roomId":"bedwars:solo:127.0.0.1:25565:arena1","method":"redis","address":"127.0.0.1:25565","serverName":"","arenaName":"arena1","mode":"solo","channel":"bedwars:match","playerCount":1,"maxPlayers":8,"state":"WAITING","motd":"","collectedAt":1000,"expiresAt":16000}
```

- Redis roomId 保留完整原始 Hash key, 不用 arenaName 当跨服唯一键. address 保留游戏服上报地址, 不做 ip-map替换后发布
- MOTD roomId 为 `motd:<address>`, method 为 motd, serverName 为 groups.yml 配置的 Bungee 服务名, arenaName/mode/channel为空, state 为 ONLINE 或 OFFLINE
- MOTD 的 motd 已递归拼接 text/extra/数组并清除颜色代码, 模式使用 DOTALL 的 find 语义. Go 与 Java 共同支持的正则子集, 配置不支持 Go 不能编译的回溯、反向引用或前后查找
- 房间可加入需状态允许、人数合法且小于 maxPlayers、expiresAt尚未到期. MOTD 另需命中motdPatterns, 空patterns允许已配置服务的在线目标

Group 全部字段(rooms为空时可省略, 客户端按空列表处理):

```json
{"name":"bw-solo","method":"redis","modes":["solo"],"joinableStates":["WAITING"],"channel":"bedwars:match","keyPrefix":"bedwars:","serverFilter":[],"arenaExclude":[],"servers":[],"motdPatterns":[],"rooms":[]}
```

serverFilter对完整address、arenaExclude对arenaName进行整个字符串glob匹配, 支持 * 和 ?. 模式以其他字符原样匹配, 不当作正则. Redis默认状态WAITING, MOTD默认状态ONLINE. mode/modes在YAML中二选一, JSON统一为modes

Definition 为 `{schemaVersion,configVersion,ticketTtlMillis:60000,groups:[Group无rooms],servers:[{name,address}]}`. configVersion为标准化分组定义的SHA256小写hex, 故障原Hash读取必须使用这份对应版本定义

Snapshot 为 `{schemaVersion,configVersion,collectedAt,expiresAt,groups:[Group含rooms]}`. HTTP与Redis返回同一结构, 不把过期快照续期, 不发布SCAN预算耗尽或Redis错误产生的部分快照. 单个原Hash正常过期跳过, SLP离线作为OFFLINE记录

Ticket 为:

```json
{"schemaVersion":1,"token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","playerUuid":"00000000-0000-0000-0000-000000000001","group":"bw-solo","room":{"roomId":"bedwars:solo:127.0.0.1:25565:arena1","method":"redis","address":"127.0.0.1:25565","serverName":"","arenaName":"arena1","mode":"solo","channel":"bedwars:match","playerCount":1,"maxPlayers":8,"state":"WAITING","motd":"","collectedAt":1000,"expiresAt":16000},"issuedAt":1000,"expiresAt":61000}
```

示例token仅用于结构测试, 不是生产凭据. Handler检查schemaVersion、标准UUID、票据时限、token格式、room身份与自身原始address, 不依赖room快照15秒时限来否定60秒内已签发的票据. Handler仅观测, 无GETDEL/DEL或游戏加入副作用

## HTTP

所有业务API需要唯一 `Authorization: Bearer <MATCHCORE_API_TOKEN>`, Content-Type与响应为application/json

- GET /v1/groups: 返回完整Snapshot, 一次包含全部Group定义与rooms
- GET /v1/groups/{group}/servers: 返回只含对应Group的同版本Snapshot
- POST /v1/matches: 请求 `{playerUuid,group,arenaName}`; arenaName可省略, 成功直接返回Ticket本体. 选房先过滤状态/空位/时限, 指定地图优先, 人数最多优先, 并列随机. 指定地图无可用目标时延续原行为从该组其他目标选择

| HTTP状态 | code/含义 |
| --- | --- |
| 400 | bad_request, JSON/UUID/参数无效 |
| 401 | unauthorized, 鉴权错误 |
| 404 | group_not_found |
| 409 | no_room, 有效数据但没有可加入房间 |
| 503 | data_unavailable, 数据源/快照/票据写入不可用 |

错误响应只含code, 不回显token、密码、内部错误或任意用户输入. 401不当作网络故障绕过鉴权, 409不通过陈旧数据重新选房. HTTP返回成功前Redis SET必须成功

## 分组文件

groups.yml 使用 groups 和 servers 两个顶层对象. servers为 `Bungee服务名: {address: "host:port"}`. Group支持method、mode/modes、joinable-states、channel、key-prefix、server-filter、arena-exclude、servers、motd-patterns. MOTD显式引用servers名单, 后端不读取远端Bungee配置文件或接受HTTP任意地址

`.env` 保留既有参数, 增加 MATCHCORE_GROUPS_FILE=groups.yml、MATCHCORE_REDIS_PREFIX=matchcore:、MATCHCORE_REFRESH_INTERVAL=3s、MATCHCORE_COLLECT_TIMEOUT=5s、MATCHCORE_SNAPSHOT_TTL=15s. 票据TTL固定60秒, 不额外开放配置

原始Hash为 bedwars:{mode}:{ip}:{port}:{arenaName}, 字段playerCount/maxPlayers/state/updatedAt. TTL60秒且30秒仅续期, updatedAt不是心跳. 采集只HMGET必要状态字段并用PTTL和本次成功时间, 同一Hash在多个分组中仅采集一次. fixtures/config.json/snapshot.json/ticket.json由Go模型实际生成, 假token仅供解析, 时效测试使用ticket.issuedAt+1的可控clock
