# optimistic-store

零依赖、无响应式框架的乐观并发状态存储（TypeScript + Node.js 内置测试）。

## 设计

事件溯源模型：**状态 = 基础快照 + 操作日志重放**。每个操作携带
`(opId, version, mutations)`，先入日志再发布快照，因此天然满足：

- **乐观补丁**：`applyOptimistic` 立即入账并发布，读者马上可见。
- **失败仅撤销自身效果**：`reject` 把操作标记为已撤销，重放时跳过它——
  之后其他操作的写入（如 B 覆盖 A 写过的字段）原样保留，不会被回退覆盖。
- **确认按 opId + version 匹配**：`confirm` / `reject` 只作用于 opId 相同且
  version 一致的 pending 操作；版本不符、操作未知、已终结的响应一律忽略。
- **重复确认幂等**：对已终结操作的重复响应返回 `already-resolved`，
  不重新发布快照（快照引用都不变）。
- **迟到旧响应不回退新状态**：已确认后的迟到 `reject`、重试升级版本后的
  旧 version 响应，均被安全忽略。
- **读者看不到事务中间态**：所有变更先落在私有工作副本上，重放完成后
  一次性原子发布深冻结快照；`transact` 内多步操作只发布一次，抛错则不发布。
- **状态可回放**：`OptimisticStore.replay(base, log)` 离线重建；
  `replayTo(seq)` 重放到任意历史点（撤销按逻辑序号生效）。

## 运行

需要 Node.js ≥ 22.6（用 `--experimental-strip-types` 直接运行 TypeScript，
无任何第三方依赖；Node ≥ 23.6 可去掉该标志）。

```bash
npm test        # node --experimental-strip-types --test test/store.test.ts
npm run demo    # node --experimental-strip-types src/demo.ts
npm run typecheck  # 若本机装有 TypeScript
```

## 运行样例输出

```
0. 初始状态                                                    {"title":"draft","likes":0,"tags":[]}
1. A 乐观写 title=「A 的标题」(op-A@v1, pending)                   {"title":"A 的标题","likes":0,"tags":[]}
2. B 随后写 title=「B 的标题」(op-B@v1, pending)                   {"title":"B 的标题","likes":0,"tags":[]}
3. 服务器拒绝 op-A@v1 → 仅撤销 A，B 的标题保留                     {"title":"B 的标题","likes":0,"tags":[]}
4. 错误版本的确认 confirm(op-B, 99) → {"applied":false,"opId":"op-B","reason":"version-mismatch","currentVersion":1}
   状态不受影响                                                {"title":"B 的标题","likes":0,"tags":[]}
5. 正确确认 confirm(op-B, 1) → op-B 落账                         {"title":"B 的标题","likes":0,"tags":[]}
6. 重复确认 confirm(op-B, 1) → {"applied":false,"opId":"op-B","reason":"already-resolved","currentStatus":"confirmed"} | 快照引用不变: true
7. 迟到的 reject(op-C, 1)（已确认）→ {"applied":false,"opId":"op-C","reason":"already-resolved","currentStatus":"confirmed"}
   likes 保持 1，未回退                                        {"title":"B 的标题","likes":1,"tags":[]}
8. 迟到的 confirm(op-D, 1)（当前 v2）→ {"applied":false,"opId":"op-D","reason":"version-mismatch","currentVersion":2}
   v2 的写入不受影响                                           {"title":"B 的标题","likes":1,"tags":["v2-标签"]}
9. 事务内 2 个操作 → 读者收到通知次数: 1 （仅最终态 1 次）
   事务发布后的状态                                            {"title":"B 的标题","likes":2,"tags":["v2-标签","tx"]}
10. 从基础状态 + 操作日志重放 == 当前状态: true
```

## 测试结果

Node.js 内置测试运行器（`node:test`），14 个用例全部通过：

```
ok 1 - 乐观补丁立即对读者可见
ok 2 - A 乐观写后 B 再写，A 失败只撤销自身效果，不覆盖 B
ok 3 - 失败撤销恢复被自身覆盖、且无人再写的字段
ok 4 - 确认响应按 opId + version 匹配，版本不符则忽略
ok 5 - 重复确认幂等：状态与快照引用均不变
ok 6 - 迟到的旧响应不回退已确认的新状态
ok 7 - 操作重试升级版本后，旧版本的迟到响应被忽略
ok 8 - 未知操作的响应被安全忽略
ok 9 - 读者看不到事务中间态：多步操作只发布一次
ok 10 - 事务抛错时什么都不发布
ok 11 - 发布给读者的快照是深度不可变的
ok 12 - 状态可从操作日志完整回放，且支持重放到任意历史点
ok 13 - compact 折叠已确认操作后不改变对外状态
ok 14 - 同一 opId 在 pending 期间禁止重复使用
# tests 14
# pass 14
# fail 0
```

## API 一览

| 方法 | 说明 |
| --- | --- |
| `applyOptimistic(opId, version, mutations)` | 乐观入账并立即发布 |
| `confirm(opId, version)` / `reject(opId, version)` | 按 opId+version 匹配终结操作 |
| `transact(fn)` | 多步操作原子发布，抛错则不发布 |
| `state` / `subscribe(fn)` | 读取深冻结快照 / 订阅发布 |
| `replayTo(seq)` / `OptimisticStore.replay(base, log)` | 历史点重放 / 离线重建 |
| `compact()` | 已确认操作折叠进基础状态 |
