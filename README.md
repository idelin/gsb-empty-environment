# 响应式环路死锁保护器（reactive-circular-guard）

带周期检测的响应式循环更新守护器。在 `Signal` / `Computed` / `Effect`
响应式模型之上提供守护模块，检测在 Effect 或 Computed 中**直接或间接写入
自身依赖**的恶意逻辑；当检测到**调用栈环路**（节点重入）或**迭代次数突破
阈值**（默认第 50 次迭代）时强制阻断，抛出 `CircularSignalUpdate` 异常并
输出详细的循环因果链报告。

## 环境要求

- Node.js >= 22.6（推荐 >= 23.6，原生 TypeScript 类型擦除运行，无需编译）
- Node 22.6 ~ 22.17 需加 `--experimental-strip-types` 运行
- 纯 TypeScript 编写（仅使用可擦除语法），无第三方依赖

## 运行

```bash
npm test        # node --test test/*.test.ts（Node 内置测试运行器）
npm run demo    # node examples/demo.ts（运行样例）
```

## 使用示例

```ts
import { signal, effect, CircularSignalUpdate } from './src/index.ts';

const a = signal(0, { name: 'A' });
try {
  effect(() => {
    a.set(a.get() + 1); // Effect 读取 Signal A 同时写入 Signal A
  }, { name: 'self-increment' });
} catch (error) {
  if (error instanceof CircularSignalUpdate) {
    console.log(error.message); // 完整循环因果链报告
  }
}
```

## 检测能力

- **直接自依赖**：Effect/Computed 在执行中写入本次运行读取过的 Signal。
- **间接自依赖**：Effect 读取 Computed（其依赖 Signal A），却写入 Signal A，
  沿依赖闭包识别为间接自依赖。
- **调用栈环路**：Computed 互相读取导致同一节点在执行栈上重入，立即抛出
  （`kind: 'call-stack-cycle'`）。
- **迭代阈值**：单次更新级联中 Effect 调度执行次数达到阈值（默认 50，
  可通过 `configureGuard({ maxIterations })` 调整）即抛出
  （`kind: 'iteration-overflow'`）。

## 异常报告

`CircularSignalUpdate` 携带结构化报告（`error.report`）与格式化文本
（`error.message`），包含：阻断原因、触发源（写入方 + 被写 Signal +
直接/间接关系）、被拦截节点、完整循环因果链（read / write / notify / run
事件序列，超长自动折叠中间段）。

## 运行样例输出（节选）

```
=== 2. 死循环场景（Effect 读写 Signal A） ===
已捕获 CircularSignalUpdate，A 最终值 = 49

检测到响应式循环更新（死循环），守护器已强制阻断。
阻断原因: 迭代次数突破阈值（在第 50 次迭代时拦截，阈值 = 50）
触发源: Effect#4 "self-increment" 在执行中写入了自身依赖 Signal#3 "A"（直接自依赖）
被拦截节点: Effect#4 "self-increment"
循环因果链（共 196 条事件）:
     1. [run   ] Effect#4 "self-increment" 开始执行
     2. [read  ] Effect#4 "self-increment" 读取 Signal#3 "A" (value=0)
     3. [write ] Effect#4 "self-increment" 写入 Signal#3 "A": 0 -> 1  << 自依赖写入（直接）>>
     4. [notify] Signal#3 "A" 变更，调度 Effect#4 "self-increment"
   ... 省略中间 176 条事件 ...
   196. [notify] Signal#3 "A" 变更，调度 Effect#4 "self-increment"
```

## 测试结果

Node v23.9.0 下 `node --test test/*.test.ts`：9 个测试全部通过。

```
✔ 正常场景：Effect 读取 A 写入 B，有限次迭代后收敛，不触发守护
✔ 正常场景：条件自增在阈值内收敛，不触发守护
✔ 验收场景：Effect 读取 Signal A 同时写入 A，在第 50 次迭代被拦截并指出触发源
✔ 间接自依赖：Effect 读取 Computed（依赖 A）却写入 A，被识别为间接自依赖
✔ 调用栈环路：两个 Computed 互相读取，立即抛出 call-stack-cycle
✔ 跨节点环路：两个 Effect 互相触发，迭代突破阈值被拦截
✔ 迭代阈值可配置：maxIterations = 5 时在第 5 次迭代拦截
✔ 守护器阻断后系统仍可正常使用（状态已复位）
✔ dispose 后 Effect 不再被调度
ℹ pass 9 / fail 0
```

## 目录结构

```
src/errors.ts    CircularSignalUpdate 异常与因果链报告格式化
src/guard.ts     守护器核心：级联跟踪、迭代计数、执行栈重入检测、自依赖记录
src/reactive.ts  Signal / Computed / Effect 响应式实现（接入守护器）
src/index.ts     公共导出入口
test/circular-guard.test.ts  Node 内置测试（node:test + node:assert）
examples/demo.ts 运行样例
```
