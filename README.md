# 多优先级协程池（Coroutine Pool）

TypeScript 实现的协程池：优先级抢占 + 时间片轮转 + 执行步数配额监控。
仅依赖 Node.js 内置模块（`node:test` / `node:assert`），利用 Node ≥ 23.6 的原生
TypeScript 类型擦除直接运行 `.ts`，无需编译步骤与第三方依赖。

## 调度策略

- 协程体为生成器，每次 `yield` 记为一个**执行步**，并产生一条执行轨迹标签。
- **优先级抢占**：协程主动 Yield 后，若就绪队列中存在更高优先级协程，立即插队
  （优先级数值越大越紧急）。
- **时间片轮转**：同优先级协程按时间配额（`timeSlice`，默认 3 步）先进先出轮转。
- **配额监控 / 活性保护**：协程连续占满配额且存在同优先级就绪协程时，强制协同
  切换；高优先级协程不受配额限制，可连续运行至完成，严格杜绝优先级反转。

## 文件

| 文件 | 说明 |
| --- | --- |
| `scheduler.ts` | `CoroutinePool`：`spawn` / `run` / `trace` / `events` / `stats` |
| `sample.ts` | 运行样例（A/B 抢占 + C/D 轮转） |
| `scheduler.test.ts` | Node.js 内置测试（6 个用例） |

## 运行

```bash
npm run sample   # 或 node sample.ts
npm test         # 或 node --test scheduler.test.ts
```

## 运行样例输出（节选）

```
执行轨迹:
   1. A:step-1
   2. A:yield        <- A(prio=1) 主动出让，B(prio=3) 已就绪
   3. B:step-1       <- B 立即插队
   4. B:step-2
   5. B:step-3       <- B 超时间片仍不被低优先级打断
   6. A:step-2       <- B 完成后 A 继续
   7. A:step-3
   8. C:step-1       <- 同优先级 C/D 按配额(2)轮转
   9. C:step-2
  10. D:step-1
  11. D:step-2
  12. C:step-3

调度事件: preempt:A  done:B  rotate:A  rotate:C  rotate:D  done:A  done:C  done:D
```

## 测试结果

```
✔ 验收边界：A(prio=1) 一次 Yield 立即让 CPU 给就绪的 B(prio=3)，B 完成后 A 继续
✔ 优先级反转控制：高优先级协程超出时间片仍连续运行至完成，不被低优先级打断
✔ 时间片轮转：同优先级协程按配额交替执行，强制协同切换保护活性
✔ 配额监控：低优先级长协程独占时持续运行，同优先级伙伴出现后按配额让位
✔ 调度顺序：就绪队列按优先级降序、同优先级先进先出
✔ 参数校验：非法时间片与重复 id 抛错
ℹ tests 6  ℹ pass 6  ℹ fail 0
```
