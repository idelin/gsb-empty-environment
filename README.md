# stream-transform-pipeline

流式记录变换管道，TypeScript 实现，零依赖，使用 Node.js 内置 `node:test` 验证。

## 特性

- **两种模式**
  - `atomic`（整批原子）：任一记录失败即抛 `BatchError`，不发布任何结果、不写检查点。
  - `isolated`（逐条隔离）：坏记录产出 `error` 事件并登记台账，后续好记录照常产出。
- **错误可定位**：`TransformError` 携带 `recordId`、`step` 与 `path`（如 `$.items[1].price`），
  用 `getAtPath(record, err.path)` 可取回出错值。
- **顺序保持**：逐条顺序处理，输出顺序与输入顺序一致。
- **检查点恢复**：产出即登记 `emittedIds`，恢复后已产出记录不重复产出；
  错误台账按记录 ID 去重，恢复后错误记录不被重复计数；重试成功的记录自动移出台账。
- **JSON 兼容树**：对象键无序（`jsonEquals` 键序无关）、数组有序（顺序敏感）。

## 环境要求

Node.js ≥ 23.6（原生运行 TypeScript，无需编译）；Node 22.6+ 可加
`--experimental-strip-types` 运行。

## 运行

```sh
npm run demo   # 运行样例 examples/demo.ts
npm test       # node --test test/*.test.ts
```

## 结构

- `src/json.ts` — `JsonValue` 树、路径格式化 / 取值、键序无关深比较
- `src/errors.ts` — `TransformError`（记录 ID + 步骤 + 路径）、`BatchError`
- `src/checkpoint.ts` — 检查点数据与内存 / 文件两种存储（文件存储原子改名写入）
- `src/pipeline.ts` — `StreamPipeline`：异步生成器产出 `record` / `error` / `checkpoint` 事件
- `examples/demo.ts` — 隔离模式、原子模式、崩溃恢复三个样例
- `test/pipeline.test.ts` — 11 个 `node:test` 用例

## 用法示例

```ts
import { StreamPipeline, type TransformStep } from "./src/index.ts";

const validate: TransformStep = {
  name: "validate",
  apply(data, ctx) {
    if (typeof ctx.at(["user", "name"]) !== "string") {
      ctx.fail(["user", "name"], "user.name must be a string");
    }
    return data;
  },
};

const pipeline = new StreamPipeline({ steps: [validate], mode: "isolated" });
for await (const event of pipeline.run(records)) {
  // event: record | error | checkpoint
}
```

## 测试结果

```
✔ isolated：输出顺序与输入顺序一致
✔ isolated：坏一条不丢后续好记录
✔ 错误携带记录 ID、步骤名与具体路径，且路径可取到出错值
✔ atomic：任一失败不发布任何结果，抛 BatchError
✔ atomic：全部成功才发布，且顺序保持
✔ 恢复后已产出记录不重复产出
✔ 恢复后错误记录不被重复计数
✔ 恢复后重试成功的记录从错误台账移除并正常产出
✔ 文件检查点跨进程式恢复：不重复产出
✔ 对象键序无关、数组顺序保留
✔ checkpoint 事件序号单调、统计一致
ℹ tests 11  ℹ pass 11  ℹ fail 0
```
