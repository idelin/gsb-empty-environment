# revision-history

线性撤销/重做历史库（TypeScript），支持异步恢复与容量裁剪。

## 语义

- `push(state)`：丢弃当前前向分支，追加新条目（新 `id`，`revision` 从 1 开始），超出容量时从最旧端裁剪。
- `replace(state)`：原地改写当前条目——**保留条目 `id`，`revision` 增一**。
- `back()` / `forward()`：移动游标，两端钳位；返回移动后的当前条目（空历史返回 `undefined`）。
- `restore(ref, loader)`：异步恢复。`loader` 的结果只有在条目仍存在且其 `revision`
  仍等于 `ref.revision` 时才会应用（应用后 `revision` 增一）；否则作为旧响应丢弃。
  Promise  resolve 为 `true`（已应用）或 `false`（已丢弃）；`loader` 抛错则 reject。

```ts
import { History } from "./src/history.ts";

const history = new History<string>({ capacity: 100 });
const entry = history.push("draft");
history.replace("draft v2");        // id 不变，revision 2
history.back();
history.push("new branch");         // 前向分支被丢弃

// 异步恢复：结果绑定 (id, revision)
const applied = await history.restore(entry, async (ref) => loadState(ref.id));
```

## 测试

测试用**数组参考模型**（`test/history.test.mjs` 中的 `Model`：条目数组 + 游标）逐步对照，
覆盖连续后退钳位、旧恢复响应（replace 后、前向分支截断后、容量裁剪后、并发恢复竞争）、
容量裁剪，以及跨多种容量的随机操作模糊对照。

```sh
npm test        # 先擦除类型生成 test/.build/history.mjs，再运行测试
npm run typecheck   # 需要本机可安装 typescript 时执行 tsc --noEmit
```

说明：当前环境离线且无 TypeScript 编译器，`scripts/strip-types.mjs` 对
`src/history.ts`（仅使用可擦除类型语法）做类型擦除后直接以 Node 运行同一套逻辑。
