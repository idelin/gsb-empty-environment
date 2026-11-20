/**
 * 热切换后的标签对可达性分析。
 *
 * 语义：切换前连接按旧策略处理了任意前缀的报文，因此切换瞬间
 * 连接标签可以是旧策略从初始标签 0 可达的任意值。切换后新旧
 * 策略从同一旧标签并行继续：状态为标签对 (旧标签, 新标签)，
 * 从 (0, 0) 出发，对每个抽象类别、每个方向做广度优先搜索，
 * 得到全部可达标签对及其见证轨迹。
 */

import { packetFor } from "./classes.ts";
import type { ClassInfo } from "./classes.ts";
import { LABEL_COUNT, evalPolicy } from "./model.ts";
import type { CompiledPolicy, Direction } from "./model.ts";

export interface StepOutcome {
  rule: number;
  allow: boolean;
  label: number;
}

export interface StepRecord {
  direction: Direction;
  before: [number, number];
  oldOutcome: StepOutcome;
  newOutcome: StepOutcome;
  after: [number, number];
}

export interface PairReach {
  oldLabel: number;
  newLabel: number;
  /** 从 (0, 0) 到该标签对的见证轨迹（(0,0) 自身为空数组）。 */
  trace: StepRecord[];
}

const DIRECTIONS: Direction[] = ["forward", "reverse"];

function stepLabels(
  oldPolicy: CompiledPolicy,
  newPolicy: CompiledPolicy,
  cls: ClassInfo,
  direction: Direction,
  before: [number, number],
): { step: StepRecord; after: [number, number] } {
  const pkt = packetFor(cls, direction);
  const oldOutcome = evalPolicy(oldPolicy, pkt, before[0]);
  const newOutcome = evalPolicy(newPolicy, pkt, before[1]);
  // 拒绝不改变标签；切换前的旧标签只被旧策略的允许动作推进。
  const after: [number, number] = [
    oldOutcome.allow ? oldOutcome.label : before[0],
    newOutcome.allow ? newOutcome.label : before[1],
  ];
  return {
    step: { direction, before, oldOutcome, newOutcome, after },
    after,
  };
}

/** 计算一个抽象类别内全部可达标签对（含见证轨迹）。 */
export function reachablePairs(
  oldPolicy: CompiledPolicy,
  newPolicy: CompiledPolicy,
  cls: ClassInfo,
): PairReach[] {
  const key = (lo: number, ln: number) => lo * LABEL_COUNT + ln;
  const visited = new Set<number>([key(0, 0)]);
  const parent = new Map<number, { prev: number; step: StepRecord }>();
  const queue: [number, number][] = [[0, 0]];

  while (queue.length > 0) {
    const state = queue.shift() as [number, number];
    for (const direction of DIRECTIONS) {
      const { step, after } = stepLabels(oldPolicy, newPolicy, cls, direction, state);
      const k = key(after[0], after[1]);
      if (visited.has(k)) continue;
      visited.add(k);
      parent.set(k, { prev: key(state[0], state[1]), step });
      queue.push(after);
    }
  }

  const pairs: PairReach[] = [];
  for (const k of visited) {
    const oldLabel = Math.floor(k / LABEL_COUNT);
    const newLabel = k % LABEL_COUNT;
    const trace: StepRecord[] = [];
    let cursor = k;
    while (cursor !== key(0, 0)) {
      const entry = parent.get(cursor);
      if (!entry) break;
      trace.push(entry.step);
      cursor = entry.prev;
    }
    trace.reverse();
    pairs.push({ oldLabel, newLabel, trace });
  }
  pairs.sort((a, b) => a.oldLabel - b.oldLabel || a.newLabel - b.newLabel);
  return pairs;
}
