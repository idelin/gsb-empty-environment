# tx-command-store

命令式事务存储（TypeScript，零依赖，无响应式框架）。Node.js 23+ 原生运行
TypeScript（类型擦除），测试使用 Node 内置测试运行器 `node:test`。

## 设计

- **命令**（`src/command.ts`）：`execute` / `undo`（逆操作）/ `expectedVersion`（前置版本）。
- **原子提交**（`Store.commit`）：一组命令在私有草稿上顺序执行，成功才一次性换入
  已提交状态并递增版本，产生单条历史记录。
- **失败回滚**：任一命令失败，已执行部分按**反向**顺序调用 `undo` 回滚，
  抛 `CommandFailedError`（含已回滚命令列表），已提交状态与版本不变。
- **撤销**（`Store.undo`）：对分支顶端提交的逆操作按反向重放；`undoCommit` 撤销
  非顶端提交时版本不一致，抛 `VersionConflictError` 而非误改。
- **读者隔离**：`getState()` 返回已提交状态的深拷贝并深冻结，事务中间态不可见。
- **分支**：历史是提交节点 DAG，分支仅持有顶端指针，互不覆盖；`checkout` 通过
  `replay` 从初始状态重放该分支命令序列重建状态（状态操作可回放）。

## 运行

```sh
npm run demo   # node examples/demo.ts
npm test       # node --test 'test/*.test.ts'
```

## 运行样例输出

```
== 1. 组合命令原子提交（转账 = 借 + 贷，单条历史） ==
balances: {"alice":70,"bob":80}, version: 1

== 2. 中间命令失败，已执行部分回滚 ==
failed: debit(bob, 9999), rolled back: [debit(alice, 20)]
balances unchanged: {"alice":70,"bob":80}, version: 1

== 3. 撤销：逆操作反向执行，组合逆操作完整 ==
before undo: {"alice":60,"bob":95}
after undo:  {"alice":70,"bob":80}

== 4. 撤销前状态已改变 -> 冲突而非误改 ==
conflict: cannot undo c3: state has changed (version 3 != 2)
balances untouched: {"alice":78,"bob":80}

== 5. 分支历史互不覆盖 ==
feature: {"alice":78,"bob":80,"carol":200}
main:    {"alice":78,"bob":80} (carol 不存在)
main history:    [c4,c3,c1]
feature history: [c5,c4,c3,c1]

== 6. 状态可回放：replay 与当前状态一致 ==
replayed == committed: true

== 7. 读者看不到事务中间态 ==
reader saw alice=78 during tx, committed alice=68
```

## 测试结果

```
✔ 命令执行并提交，版本递增
✔ 前置版本匹配才允许执行，不匹配则冲突且不产生副作用
✔ 多个命令组合为原子提交：单条历史、单次版本递增
✔ 中间命令失败：已执行部分按反向回滚，状态与版本不变
✔ 撤销顺序反向：逆操作按命令逆序执行
✔ 组合后逆操作完整：undo 组合提交后回到提交前状态
✔ 撤销前状态已改变：抛冲突而非误改，状态保持不动
✔ 读者快照：事务中间态不可见，快照被冻结
✔ 分支历史互不覆盖
✔ 状态可回放：replay 重建结果与当前提交状态一致
✔ checkout 后撤销基于该分支版本，跨分支旧提交撤销冲突
ℹ tests 11  ℹ pass 11  ℹ fail 0
```
