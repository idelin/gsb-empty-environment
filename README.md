# atomic-command-store

命令模式 + 原子事务 + 版本前置校验 + 分支历史，零依赖（仅用 Node 23 内置类型擦除运行 TypeScript）。

## 设计

- **命令三要素**：`execute`（执行）、`undo`（逆操作）、`expectedVersion`（前置版本），见 `src/store.ts` 的 `Command` 接口。
- **原子提交**：`store.commit([...])` 在私有暂存副本上依次执行，全部成功才一次性换入可见状态——读者（`store.read()`）只能看到提交前/后的冻结快照，永远看不到中间态。
- **失败回滚**：任一命令失败或前置版本不满足，已执行部分按**反向顺序**用各自逆操作回滚，并校验暂存区精确还原，否则抛 `InverseIncompleteError`。
- **撤销**：`store.undo()` 反向应用最近一次提交的全部逆操作；若当前版本与该提交不匹配（状态已被外部改变），抛 `ConflictError` 而非误改。
- **分支**：`createBranch` / `checkout`，每个分支拥有独立的状态、版本与历史，未选分支的历史不会被覆盖。
- **可回放**：所有提交/撤销追加到分支日志，`store.replay()` 从分支起点重放可精确重建当前状态。

## 运行

```sh
npm test        # Node 内置测试运行器，12 个用例
npm run sample  # 场景演示
```

要求 Node >= 23.6（默认启用 TypeScript 类型擦除），无任何 npm 依赖。
