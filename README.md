# 句柄复制扩大了对象访问掩码

代理进程替沙箱复制内核对象句柄时，若信任请求中的desiredAccess或复用后的句柄号，会把只读对象变成可写。给Go宿主实现可导入的句柄表模型：进程有唯一processID与代次，句柄槽含objectID、对象代次、grantedAccess、inherit位和句柄代次；对象类型定义可复制权限掩码及GenericRead/Write到具体权限位的固定映射，先展开再比较。Duplicate请求绑定源进程/代次、源句柄/代次、目标进程/代次、desiredAccess和sameAccess标志；先将generic位规范映射，sameAccess与非零desiredAccess共存拒绝，否则新权限必须是源grantedAccess与对象可复制掩码的子集。源句柄在复制线性化点须仍打开且对象代次一致，目标槽由宿主分配并产生新句柄代次；关闭后槽号可复用但旧请求失效。跨完整性级别复制还需调用者持有宿主签发的broker能力，能力绑定两进程代次和对象类型。批量复制全部验证后原子发布，任何失败不留下部分句柄。用小状态BFS测试close/reuse竞态、generic映射扩权、sameAccess冲突、目标进程重启及伪造broker能力，输出最短能获得源未授予Write的待审实现轨迹。

## 实现说明

- `types.go`：Access 位、ObjectType（CopyableMask + Generic 固定映射，`Expand` 先展开再比较）、ProcessRef/HandleRef（ID+代次）、Capability（绑定两进程代次与对象类型，宿主 MAC 签发）、DuplicateRequest。
- `host.go`：已审宿主 `Host`。`validate` 在线性化点校验进程/句柄/对象代次、broker 能力、sameAccess 冲突与子集约束（新权限 ⊆ 源 grantedAccess 且 ⊆ 可复制掩码）；`publish` 由宿主分配目标槽并产生新句柄代次；`DuplicateBatch` 先全部验证再原子发布。
- `review.go`：待审实现 `ReviewHost`（DO NOT SHIP），故意信任 desiredAccess、忽略各类代次与能力 MAC、批量泄露部分结果，文件头注释列出全部缺陷。
- `modelcheck.go`：`System` 小步接口 + 通用 BFS 引擎（保证最短反例轨迹）。
- `modelcheck_test.go`：小状态场景（高完整性 victim 持只读句柄、低完整性 attacker），对五类缺陷分别 BFS；`TestSecureHostHasNoViolation` 断言安全宿主在深度 5 内无违规，`TestGenerateTraceReport` 重新生成 `TRACES.md`。
- `TRACES.md`：待审实现各类别最短违规轨迹（由测试生成）。

运行：`go test ./... -v`
