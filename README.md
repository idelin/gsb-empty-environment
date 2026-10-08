# offline-replay

离线编辑重放库：离线期间产生的编辑操作可引用临时对象 id（默认前缀 `temp:`），
联网后由 `ReplayEngine` 按依赖拓扑顺序发往服务器，并保证：

- **依赖拓扑执行**：操作 payload 中出现的 temp id 自动构成对创建该 id 的操作
  （`provides` 声明）的依赖，也可用 `dependsOn` 显式声明；依赖未满足的操作不会被发送。
- **失败隔离**：某操作失败仅将其传递后继标记为 `blocked`，独立操作继续执行。
- **递归 id 替换**：服务器回执携带 `idMap`（temp id → 服务器 id）后，
  所有尚未发送的操作 payload 会被递归重写（嵌套对象 / 数组均覆盖）。
- **重复回执幂等**：对已终结（acked/failed）操作的回执直接忽略；
  服务器端按 opId 去重，重发返回原回执。
- **重启恢复**：所有状态变更先写入 `Journal`（内存 / 文件实现），
  崩溃重启后 `sent` 但未确认的操作会安全重发。

## 运行测试

```sh
npm test   # node --test 'tests/*.test.ts'（Node >= 23，原生类型擦除，无需编译）
```

## 用法

```ts
import { ReplayEngine } from "./src/engine.ts";
import { FileJournal } from "./src/journal.ts";

const engine = await ReplayEngine.open(new FileJournal("journal.json"), transport);
await engine.enqueue({ id: "c1", kind: "create", provides: ["temp:1"] });
await engine.enqueue({ id: "u1", kind: "update", payload: { id: "temp:1", v: 2 } });
const summary = await engine.flush(); // { acked, failed, blocked, pending }
```

`Transport` 是唯一外部依赖：实现 `send(op): Promise<Receipt>` 即可接入真实后端。
引用未知 temp id（无对应 `provides` 操作）的操作会保持 `pending`，绝不会把临时 id 发往服务器。

## 结构

- `src/engine.ts` — 重放引擎（拓扑调度、失败级联、回执应用、持久化）
- `src/id-rewrite.ts` — temp id 引用收集与递归重写
- `src/journal.ts` — `MemoryJournal` / `FileJournal`（原子写）
- `src/types.ts` — `Operation` / `Receipt` / `Transport` 等类型
- `tests/fake-server.ts` — 可控返回值的幂等假服务器（success / failure / drop）
- `tests/replay.test.ts` — 拓扑顺序与递归替换、部分失败隔离、显式依赖级联、
  重复回执幂等、崩溃重启恢复、悬空引用保护
