# 跨 DST 值班表求解与多级升级时间线引擎

PagerDuty 风格的企业级 SRE 值班响应核心库，纯 TypeScript、零运行时依赖，
仅依赖运行时内置的全 ICU `Intl`（IANA tzdata）。可编译为 ESM 后导入使用。

## 解决的三类易错边界

1. **多图层值班表与跨 IANA DST 的本地墙上时间对齐**
   - 多个 Base Rotation 图层（按日 / 周 / 自定义时长）按图层所在 IANA 时区
     的本地墙上时间交接，绝不简单加减 `86400s`。
   - 美东 / 欧洲春令时跳空日交接间隔自动为 **23h**，秋令时重叠日为 **25h**。
   - Restriction 周期窗（如当地周一至周五 09:00–18:00），跨午夜自动拆段。
   - 叠加规则严格为 `Override > 高序图层 > 低序图层`；局部 Override 把原
     值班区间精准劈为三段，相邻同人区段自动合并，最终输出无重叠的精确
     UTC 时间线。
2. **多级升级与空班即时跳级**
   - `L1..Lk` 各带 `delayMinutes` 与目标（具体用户或多个值班表）。
   - 某层级因 Restriction 解析出空目标集时，记录 `level_skipped_empty` 并
     **立刻顺延**，不空等延时；全部层级皆空时立刻 `unassignable`，遍历
     有界，杜绝死循环。
3. **循环升级、Snooze 与状态撤销**
   - `numLoops` 控制到末级后回卷 `L1` 的轮次上限。
   - `Ack` 立即取消待发计时器；`Snooze(duration)` 静默，到期自动恢复为
     未确认并重新计时；`Resolve` 永久终结全部调度事件。

## 目录结构

```
src/
  types.ts       领域模型与事件类型
  timezone.ts    DST 安全的墙上时间 <-> UTC 瞬时换算（gap/overlap 消歧）
  schedule.ts    轮换生成、Restriction 掩码、Override 劈分与同人合并
  clock.ts       虚拟/系统时钟与最小堆计时器
  escalation.ts  多级升级状态机、空班跳级、循环、Ack/Snooze/Resolve
  index.ts       统一导出
tests/           零依赖测试套件（32 项）
docs/usage.md    带注释的使用示例
```

## 构建与测试

本环境的 Node 未启用 TypeScript 原生执行，因此先经全局 `tsc` 编译到
`dist/`，再由 Node 运行（无需联网安装依赖）。

```bash
npm run build   # tsc 编译，产出 dist/ 下的 .js / .d.ts / sourcemap
npm test        # 先构建再运行全部测试（32 项）
```

导入：

```ts
import {
  buildTimeline,
  whoIsOnCall,
  EscalationEngine,
  ScheduleTargetResolver,
  VirtualClock,
} from "./dist/src/index.js";
```

## 关键设计

- **DST 换算**：`zonedPartsToUtc` 采用“双偏移定点”判定常态 / 跳空 /
  重叠。春令时不存在的墙上时间前移到首个有效瞬时；秋令时重复的墙上时间
  默认取较早瞬时，可显式取较晚瞬时。所有轮换步进都在墙上日历组件上做
  加法后重新解析为瞬时，从而自然得到 23h/25h。
- **时间线合成**：收集所有区间边界做扫描，在每个基本切片内按
  `Override > 图层 priority > 数组次序` 选胜者，丢弃无人覆盖的切片，并
  合并相邻同用户段，保证结果无重叠、半开 `[start, end)`、完全有序。
- **升级引擎**：注入式 `Clock` + 最小堆 `Scheduler`，使时间线确定性可
  回放。位置被扁平化为 `loop * levelCount + levelIndex`，空班遍历有界；
  目标解析推迟到计时器真正触发时，反映值班状态随时间的变化。

## 验收覆盖

| 验收点 | 测试文件 |
| --- | --- |
| 美东/欧洲 23h、25h 交接对齐 | `tests/timezone.test.ts`, `tests/dst-timeline.test.ts` |
| gap 前移、overlap 早晚瞬时 | `tests/timezone.test.ts` |
| Override 一裂三与同人合并 | `tests/schedule.test.ts` |
| Restriction 跨午夜拆段、空班无人 | `tests/schedule.test.ts`, `tests/integration.test.ts` |
| 空班即时跳级与全空班防死循环 | `tests/escalation.test.ts` |
| numLoops 轮次上限 | `tests/escalation.test.ts` |
| Ack/Snooze/Resolve 计时器撤销与重激活 | `tests/escalation.test.ts` |
