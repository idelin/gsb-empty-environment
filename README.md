# 分布式随机种子承诺揭示

安全演练服务需要在所有参与方承诺后共同生成不可被最后一方替换的随机种子。写供Go宿主调用的轮次包，固定成员名单，承诺为带域分隔的SHA256，揭示必须匹配承诺，全部揭示后按成员ID排序拼接再哈希；任何成员缺席只能中止，禁止把缺席当零值。轮次事件持久化且可重放，重复相同消息幂等、冲突保留证据。用并发提交和逐次掉电测试不提前泄露种子，不跨轮使用揭示，明确不保证缺席时仍能产出。

## `round` 包

实现位于 `round/`，仅依赖 Go 标准库，供宿主以 `Start` / `Replay` 驱动。

- 固定名单：`Config{ID, Members}` 在创建事件中固化，重复/空成员 ID 直接拒绝，名单后续不可变。
- 域分隔承诺：`Commitment` 为 `SHA256("netsec-round/v1/commit" || NUL || len(roundID)… || len(member)… || len(secret)…)`，长度前缀消除拼接歧义，轮次 ID 和成员 ID 绑定使承诺/揭示无法跨轮或跨成员重放。
- 揭示校验：揭示必须复现该成员已记录的承诺；首个有效消息生效，不同的承诺/揭示被拒绝并以 `commit_conflict`、`reveal_mismatch`、`message_rejected` 等事件保留证据。揭示先于承诺到达会持久化等待，承诺到达后再校验。
- 种子派生：仅当全部成员都有匹配对时，按成员 ID 字典序计算 `SHA256("netsec-round/v1/seed" || NUL || len(roundID)… || [len(member)… || len(secret)…]…)`。任何成员缺席或未匹配时轮次保持 `open`，`Seed()` 返回 `ErrNoSeed`，绝不以零值顶替；宿主应调用 `Abort` 中止，包不保证缺席时仍能产出种子。
- 持久化与重放：所有状态转移先追加到 `EventStore`（`FileStore` 每行带 SHA256 校验和并 `fsync`），再更新内存；`Replay` 逐事件重放并独立重算种子，校验序号、轮次 ID 和时间戳。掉电最多留下一条撕裂尾行，重放时丢弃；中间行损坏返回 `ErrCorruptLog`。
- 幂等与并发：相同消息产生 `*_duplicate` 事件且状态不变；`Round` 内部互斥，并发提交/揭示安全，事件序号严格连续。
- 无提前泄露：完成事件是种子唯一出现的位置；在此之前任何事件都不含种子，`Seed()` 一律返回 `ErrNoSeed`。

### 验证

- `go test ./...`：正常流程、域分隔、揭示不匹配、冲突证据、幂等、缺席中止、跨轮拒绝、零值拒绝、乱序消息与并发提交。
- `go test -race ./...`：并发下数据竞争与最终种子确定性。
- `TestPowerLossAtEveryStep`：在每次 `fsync` 前逐次模拟掉电（含创建事件与完成事件），重放后补齐剩余消息，验证恢复出的种子与无故障运行逐字节一致且中途永不泄露。
- `TestTornTailLineIsDiscarded` / `TestCorruptMiddleLineRejected`：尾行撕裂容忍、中间行篡改必报 `ErrCorruptLog`。
