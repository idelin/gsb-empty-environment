# gitaccess

制品服务的 Git 对象读取能力（capability）包。仅依赖 Go 标准库，实现了真实的
Git 对象编码：loose 对象、pack v2 容器、OFS/REF delta 指令流，以及 SHA-1
内容摘要校验。

## 核心保证

1. **固定引用快照授权**。`Service.IssueCapability` 在签发瞬间持读锁解析每个
   引用名，把当时的目标 OID 固化进能力，并沿 commit/tree/tag 边做可达性遍历，
   得到不可变的允许集合。之后引用如何更新（包括同名替换）都不会改变已发能力的
   允许集合，见 `Capability.Snapshot` 与 `Capability.Allowed`。
2. **并发更新不放宽已发能力**。引用读写由 `Service` 的 `RWMutex` 串行化，能力
   持有的是 OID 快照而非引用名，因此更新引用只影响之后签发的能力。
3. **增量解码先过摘要再返回**。pack 条目懒解码：`Pack.materialize` 重建 delta
   链后，对最终内容重新计算 Git 帧 SHA-1，与索引中声明的 OID 比对，失败即返回
   `ErrChecksumMismatch`，任何调用方都拿不到未过摘要的内容。
4. **失败与取消不污染缓存**。每次 delta 重建使用请求级 `memo`，与能力的共享
   缓存隔离；`ReadObject` 仅在加载成功、摘要通过且 `ctx.Err() == nil` 后才写入
   能力缓存。缓存按能力（租户）隔离，返回给调用方的是副本。

## 主要类型

- `ObjectDatabase`：loose 对象与已校验 pack 的共享存储，只负责摘要，不做授权。
- `Service`：引用命名空间，负责引用更新与能力签发。
- `Capability`：单个租户的不可变读授权，内含独立的已验证对象缓存。
- `OpenPack(data, index)`：用配套索引（`.idx` 的内存等价物）解析 pack，校验
   魔数、版本、条目结构与尾部 SHA-1；内容摘要延迟到实际读取时校验。

## 合成仓库测试

测试通过 `packBuilder` 生成真实字节流，不依赖磁盘上的 git 仓库：

| 测试 | 验证内容 |
| --- | --- |
| `TestRefReplacement` | 同名引用替换后，旧能力不被放宽、新能力受新快照约束 |
| `TestConcurrentRefUpdate` | 并发替换引用与签发能力，快照始终自洽、无越权读取 |
| `TestDeltaDigestVerified` | 三级 OFS delta 链重建后仅在摘要通过时返回 |
| `TestBadDeltaBase` / `TestBadOfsDeltaBase` | REF/OFS 基底缺失时安全失败且不缓存 |
| `TestTamperedDeltaRejected` | 篡改 delta 压缩区后被摘要或结构校验拒绝 |
| `TestCancelIsolation` | 读取中途取消不留缓存，重试成功，且跨租户缓存隔离 |
| `TestAnnotatedTagReachability` | tag→commit→tree→blob 全链可达性 |
| `TestLooseDigestMismatch` / `TestPackTrailerChecksum` | loose 与 pack 完整性边界 |

运行：

```sh
go test ./gitaccess/ -race -v
```
