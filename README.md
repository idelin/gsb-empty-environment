离线协作产品删除文档后，旧副本可能带着密钥信封重新上线；同一用户之后明确创建的新文档又不能被旧删除事件误伤。请给 TypeScript 同步宿主交付可导入的文档密钥准入组件，沿用仓库里持久事件 ID 的 API 和字节表示，不能把 compareDots 的存储顺序当成因果关系。每个事件带经签名的作者身份、文档的永久对象 ID、加密代次、本地 dot 和稀疏的已观察前缀；授权创建、转授、撤回、删除以及新建代次分别有不同的签名权限。删除只能关闭它所针对的对象代次；该代次里与删除并发或晚到的旧信封均不能恢复解密资格，新的代次只有所有者签出的重建事件才能启用。副本可传压缩后的摘要与不完整后缀，缺口不可伪装成作者尚未发过事件，同一 dot 指向不同签名内容要隔离该作者相关授权。实现两个独立入口：在线合并时只维护必要证据，离线验证端依据未压缩事件重算每个主体可领取的密钥句柄集合。两者在所有合法交付顺序下须一致，未知证据只能保留待判。还要给出能丢弃旧撤销记录的精确条件，在一个副本永远不上线时也不能错误放行。测试别名文档名、多次重建、互不相见的撤回与转授、旧备份接入和 uint64 序号越过 JavaScript 安全整数边界的历史夹具。

## 交付物

- `src/dot.ts` — 持久事件 ID（`Dot` / `parseDot` / `formatDot` / `compareDots`），序号为 uint64 `bigint`。
- `src/keyadmission.ts` — 文档密钥准入组件，两个独立入口共用同一判定核心。
- `test/keyadmission.test.mjs` — 场景与等价性测试；`test/support/harness.ts` 为测试夹具。

## 数据模型与字节表示

每个 `SignedEvent` 携带：签名作者、永久对象 ID（别名文档名不进入签名与判定）、
加密代次（uint32）、作者本地 `dot`、稀疏 `observed` 前缀（actor → 已见最高连续序号）、
事件类型与载荷、Ed25519 签名。

`canonicalEventBytes` 给出长度前缀的确定性字节布局：类型字节、UTF-8 文本（4 字节大端长度
前缀）、u32 代次、dot 的 actor 文本与 u64 序号（8 字节大端，完整覆盖到 `2^64-1`，
不经过 JS `number`）、按 actor 字典序排列的 observed 条目、类型专属载荷。
`eventId` 是该字节串的 sha256 十六进制，即持久内容 ID；同 dot 不同内容即冲突。

五类事件对应五个独立签名权限（`Right`）：`grant`、`delegate`、`revoke`、
`delete`、`rebuild`。`rebuild` 额外要求作者是 `TrustRegistry.ownerOf(objectId)`
登记的所有者；非所有者即便持有 rebuild 权限也返回 `not-owner`。

## 因果与判定（不使用 compareDots）

`compareDots` 仅用于输出排序。因果关系只来自两条事实：同一作者 dot 序号连续递增，
以及事件签名的 `observed` 稀疏前缀。判定结果为三值：

- `admitted`：存在完整、签名有效、代次启用且未被否定的授权链；
- `denied`：存在完整的否定事实（代次关闭/重建冲突/作者 equivocation/被撤回）；
- `pending`：存在已证实但尚未拿到的证据，任何缺失都不得解读为“作者没发过”。

代次规则：`rebuild` 启用新一代（代次 0 为根代次）；同一代次多个 rebuild 为
`conflicted`，该代次 fail-closed。`delete` 只关闭它指向的代次；该代次内并发或晚到的
信封恒为 `denied`，旧备份重新上线也不能恢复。新一代只能由所有者 rebuild 启用，
旧 delete 不会误伤新代次。

撤回规则：`revoke` 是墓碑，杀死目标边及其转授子树。墓碑与某分支互不相见（无 observed
因果关系）时，该分支保持 `pending` 而非被杀死或放行；目标边缺失同样 `pending`。
同一 dot 出现两份不同签名内容时，该作者进入 equivocation 集合，其相关授权全部隔离
（`denied`），其他作者的授权不受影响。

## 两个入口

- 在线 `KeyAdmissionMerger`：`ingest(event)` 接受任意合法交付顺序的事件，
  `ingestSummary(FrontierSummary)` 接受签名压缩摘要 + 不完整后缀；只保留
  `EvRecord`、frontier、equivocation 与缺口集合，`view()` 输出三值视图，
  `snapshot()` 导出最小证据。
- 离线 `verifyKeyLog(objectId, rawEvents, registry)`：从完整未压缩事件自行重建
  证据图，再调用同一个 `computeView` 核心。对同一份证据，两个入口在任意交付顺序下
  输出一致（见“online merger and offline verifier agree”测试）。

未知证据（含密封摘要证明存在但未送达的前缀）只产生 `pending`；摘要签名无效返回
`bad-signature`，未知签名者返回 `unknown-summary-signer`。

## 撤回记录的丢弃条件

`canDiscardRevoke(dot, evidence)` 为真当且仅当：该墓碑所在代次已被一条仍保留的
`delete` 事件关闭。关闭的代次永远不会重新启用，delete 本身就是持久否定，墓碑才成为
冗余。代次仍开放时，即使该副本永远不再上线，墓碑也绝不可丢弃——否则旧信封会在本地
被错误放行。`discardableRevocations` 给出当前可安全丢弃的全部 dot。

## 运行测试

```
npm test
```

覆盖：别名文档名、多次重建与重建冲突、互不相见的撤回/转授、旧备份接入、压缩摘要 +
不完整后缀、equivocation 隔离、五类权限区分、在线/离线多顺序等价，以及 uint64 序号
越过 `Number.MAX_SAFE_INTEGER`（含 `2^64-1` 字节级夹具）。
