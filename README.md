# 远程执行产物验收模块

只接受**输入摘要、工具版本、尝试代次**三者都匹配的产物清单，内置缺块补取状态机与代次防护：旧尝试的成功不能覆盖已接受的新尝试。

## 状态机

```
NONE -> FETCHING -> VERIFYING -> ACCEPTED
          ^            |
          |            v (摘要不匹配)
          +------ CLEANING_UP -> (重试耗尽) FAILED
```

- `FETCHING`：清单已受理，跟踪已到达/缺失的数据块；坏块立即清除并重新计为缺失。
- `VERIFYING`：全部块到齐后拼装并校验整体摘要。
- `CLEANING_UP`：校验失败后清空该代次暂存区，回到 `FETCHING` 重试；耗尽 `maxVerifyRetries` 后进入 `FAILED`。
- `ACCEPTED`：验收水位线推进到该代次；此后 `attempt <= acceptedAttempt` 的清单与数据块一律拒绝。

## 防护规则

- 输入摘要 / 工具版本不匹配的清单直接拒绝（`INPUT_DIGEST_MISMATCH` / `TOOL_VERSION_MISMATCH`）。
- 同代次同内容清单幂等去重（`DUPLICATE`）；同代次不同内容拒绝（`MANIFEST_CONFLICT`）。
- 旧代次清单与数据块拒绝（`STALE_ATTEMPT`）；已接受代次的重复验收拒绝（`ATTEMPT_ALREADY_ACCEPTED`）。
- 更高代次清单取代进行中的旧代次，并清理其暂存内容。

## 文件

- `src/acceptance.ts` — 验收控制器、状态机、暂存区抽象。
- `src/simulate.ts` — 模拟重复清单、缺块补取、校验失败清理、代次防护。
- `test/acceptance.test.ts` — `node:test` 单元测试。

## 运行

需要 Node.js >= 22.6（TypeScript 类型擦除，无需编译）：

```sh
node --experimental-strip-types src/simulate.ts   # 运行模拟
node --experimental-strip-types --test test/      # 运行测试
```
