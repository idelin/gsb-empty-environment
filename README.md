# SWIM 故障检测度量分析仪（MTTD / 误报率）

量化 SWIM 故障检测协议运行指标的 TypeScript 分析仪：统计每个故障注入事件的
发生时刻、首次怀疑时刻、最终标记时刻与误报率，并输出详尽诊断报告。

## 环境要求

- Node.js ≥ 22.18 或 ≥ 23.6（原生 TypeScript 类型擦除，零依赖，无需编译）
- 仓库内 `.nvmrc` 指定 `23.9.0`：`nvm use` 即可

## 运行

```bash
npm start     # 运行标准场景并打印诊断报告
npm test      # 使用 Node.js 内置测试运行器 (node:test) 验证
```

## 架构

- `src/swim.ts` — SWIM 风格故障检测器模拟：每周期按轮询相位探测成员，
  连续丢失探测达到阈值即怀疑（suspect），怀疑状态持续 `confirmCycles`
  周期未恢复则标记宕机（mark dead）；恢复应答即驳斥怀疑（refute）。
- `src/faultInjection.ts` — 标准验收场景：16 节点集群、120 周期，
  注入 10 次真实节点宕机 + 5 次单周期网络丢包扰动。宕机相位由节点的
  轮询探测槽位推导：6 次宕机在 2 周期内检出、4 次在 3 周期内检出。
- `src/metrics.ts` — 度量分析仪：关联地面真值故障与检测器事件日志，
  逐事件给出发生/首次怀疑/最终标记时刻、检测延迟，汇总 MTTD 与误报率
  （误报 = 对未宕机节点的死亡标记；FPR = 误报数 / 总标记数）。
- `src/report.ts` — 文本诊断报告渲染。
- `src/main.ts` — 入口：运行标准场景并打印报告。

## 验收结果

注入 10 次真实节点宕机与 5 次短暂网络丢包扰动：

- **MTTD = 2.4 周期**（6×2 + 4×3 = 24 周期 / 10 次检出，min=2，max=3）
- **误报率 = 0%**（10 次死亡标记全部为真；5 次丢包扰动均被怀疑后在 1 周期内驳斥，从未误标）
- 10/10 宕机全部检出，0 漏报

## 运行样例

`npm start` 输出（节选）：

```
Per-Event Timeline (all cycles are protocol cycles)
---------------------------------------------------
| event    | type        | node     | occ  | susp | mark | reft | mttd | outcome |
| crash-1  | node-crash  | node-00 |   10 |   11 |   13 |    - |    3 | detected |
| loss-1   | packet-loss | node-10 |   15 |   15 |    - |   16 |    - | transient-refuted |
| crash-2  | node-crash  | node-01 |   20 |   20 |   22 |    - |    2 | detected |
| ...      | ...         | ...     |  ... |  ... |  ... |  ... |  ... | ... |
| crash-10 | node-crash  | node-09 |  100 |  100 |  102 |    - |    2 | detected |

Aggregate Metrics
-----------------
  MTTD (mean) ............. 2.40 cycles over 10 detections
  Detection latency min ... 2 cycles
  Detection latency max ... 3 cycles
  Undetected crashes ...... 0
  Transient suspicions .... 5/5 refuted
  Death marks ............. 10 (10 true, 0 false)
  False-positive rate ..... 0.0%

Conclusion
----------
  The detector identified every real crash and never marked a
  healthy member dead. All transient packet-loss suspicions were
  refuted within one cycle. Protocol robustness: CONFIRMED.
```

## 测试结果

`npm test`（Node.js 内置 `node:test`，3 个测试文件共 17 个用例）：

```
▶ acceptance: 10 node crashes + 5 transient packet-loss disturbances
  ✔ scenario injects exactly 10 crashes and 5 disturbances
  ✔ every real crash is detected
  ✔ MTTD averages 2.4 cycles
  ✔ detection latencies are 6x2 cycles and 4x3 cycles
  ✔ false-positive rate is 0%
  ✔ all 5 transient disturbances are suspected then refuted, never marked
  ✔ every event records occurrence, first-suspicion and final-mark timestamps
▶ MetricsAnalyzer
  ✔ computes MTTD as the mean of per-event detection latencies
  ✔ flags death marks on healthy nodes as false positives
  ✔ counts crashes that are never marked as undetected
  ✔ recognizes a refuted transient suspicion as correct behavior
  ✔ ignores detector events that predate the fault occurrence
▶ SwimDetector
  ✔ suspects a crashed node on the first missed probe and marks it dead after confirmCycles
  ✔ a crash after the node probe slot is detected one cycle later
  ✔ a transient packet loss is suspected then refuted, never marked dead
  ✔ healthy members never produce detector events
  ✔ dead members are no longer probed

ℹ tests 17
ℹ pass 17
ℹ fail 0
```
