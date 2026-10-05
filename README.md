# log-slicer

从最终失败断言反向追踪的日志切片工具。日志以**变量版本**（SSA 风格 `name@version`）记录读写与控制依赖，切片器沿数据依赖与控制依赖反向回溯，保留完整因果来源；对缺失的生产事件显式报缺口，并通过切片重放验证能否复现相同的断言值。

## 运行要求

Node.js ≥ 23.6（原生 TypeScript 类型擦除）。Node 22.6+ 可加 `--experimental-strip-types` 运行。

## 用法

```bash
node src/cli.ts examples/complete-log.json            # 自动取最后一个失败断言
node src/cli.ts examples/gapped-log.json --assert 8   # 指定断言事件
npm test                                              # 运行测试
```

退出码：0 = 切片重放复现断言值；1 = 存在缺口（不可验证）或重放值不一致；2 = 用法错误。

## 日志模型（`src/model.ts`）

- `write`：写 `target = name@version`，携带 `expr`（计算表达式）、`value`（记录值）、`reads`（读取的版本化变量）、`control`（控制依赖事件 id）。
- `cond`：分支判定事件，其 `id` 可被其他事件的 `control` 引用。
- `assert`：断言事件，记录 `expected` / `value`（实际值）/ `passed`。

## 设计要点

- **版本化因果来源**：生产事件按 `(name, version)` 精确索引（`src/slicer.ts` 中 `writesByVar`），绝不按变量名合并。读 `x@2` 时，即使日志里存在 `x@1`、`x@3`，也不会错误匹配，而是报缺口。
- **显式缺口**：`missing-producer`（读到的版本无写事件）与 `missing-control-event`（控制依赖指向不存在的事件）都会以 `(缺失引用, 需求方事件)` 形式记录进 `SliceResult.gaps`。
- **切片重放验证**：`src/replay.ts` 按事件序在版本化环境上重新求值 `expr`，逐事件比对记录值；断言事件的重放值必须与记录的实际值一致。存在缺口时结论为 `UNVERIFIABLE`，而非误报通过。

## 目录

- `src/model.ts` 事件与表达式类型
- `src/expr.ts` 表达式求值（重放用）
- `src/slicer.ts` 反向切片器（数据 + 控制依赖，缺口检测）
- `src/replay.ts` 切片重放与值比对
- `src/verify.ts` 切片 + 重放组合验证
- `src/cli.ts` 命令行入口
- `examples/` 完整日志与缺生产事件日志示例
- `test/slicer.test.ts` 8 个用例（含版本区分、缺口、篡改检测）
