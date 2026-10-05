/**
 * 轨迹差分引擎：按可观察动作定义对齐两条轨迹，
 * 定位首次语义分歧并还原相关内部窗口。
 */
import { TraceCursor } from './cursor';
import {
  DiffOptions,
  DiffReport,
  Divergence,
  ObservableAction,
  ObservableSpec,
  StepEvent,
  TraceEvent,
} from './types';

const DEFAULT_WINDOW_SIZE = 5;
const DEFAULT_LOOKAHEAD_LIMIT = 256;

/** 由输入定义构造可观察动作判定器 */
export function buildObservableMatcher(spec: ObservableSpec): (action: string) => boolean {
  const exact = new Set<string>();
  const prefixes: string[] = [];
  for (const pattern of spec.actions) {
    if (pattern.endsWith('.*')) prefixes.push(pattern.slice(0, -1)); // 保留 '.'
    else exact.add(pattern);
  }
  return (action: string) =>
    exact.has(action) || prefixes.some((p) => action.startsWith(p));
}

/** 固定容量的内部步骤环形缓冲（按时间升序读出） */
class RingBuffer {
  private buf: StepEvent[] = [];
  constructor(private readonly capacity: number) {}
  push(e: StepEvent): void {
    if (this.capacity <= 0) return;
    this.buf.push(e);
    if (this.buf.length > this.capacity) this.buf.shift();
  }
  toArray(): StepEvent[] {
    return this.buf.slice();
  }
}

interface SideState {
  cursor: TraceCursor;
  before: RingBuffer;
  observableCount: number;
}

/** 推进游标直到下一个可观察动作；途经的内部步骤计入窗口缓冲 */
function nextObservable(
  side: SideState,
  isObservable: (action: string) => boolean,
): ObservableAction | null {
  let item;
  while ((item = side.cursor.next()) !== null) {
    if (isObservable(item.event.action)) {
      const obs: ObservableAction = {
        action: item.event.action,
        value: item.event.value,
        stepIndex: item.stepIndex,
        observableIndex: side.observableCount,
        path: item.path,
      };
      side.observableCount += 1;
      return obs;
    }
    side.before.push(item.event);
  }
  return null;
}

/**
 * 有界前瞻：在游标未来寻找名为 name 的可观察动作。
 * 通过 clone 实现，不消耗主游标；惰性推进，绝不整体展开循环段。
 */
function lookahead(
  side: SideState,
  isObservable: (action: string) => boolean,
  name: string,
  limit: number,
): { found: boolean; foundAtOffset: number; truncated: boolean } {
  const probe: SideState = {
    cursor: side.cursor.clone(),
    before: new RingBuffer(0),
    observableCount: 0,
  };
  for (let offset = 1; offset <= limit; offset++) {
    const obs = nextObservable(probe, isObservable);
    if (obs === null) return { found: false, foundAtOffset: -1, truncated: false };
    if (obs.action === name) return { found: true, foundAtOffset: offset, truncated: false };
  }
  return { found: false, foundAtOffset: -1, truncated: true }; // 达到上限，无法确定
}

/** 收集分歧点之后紧随的内部步骤（遇到下一个可观察动作或轨迹结束即停） */
function collectAfter(
  side: SideState,
  isObservable: (action: string) => boolean,
  limit: number,
): StepEvent[] {
  const out: StepEvent[] = [];
  while (out.length < limit) {
    const probe = side.cursor.clone();
    const item = probe.next();
    if (item === null || isObservable(item.event.action)) break;
    side.cursor.next(); // 确认是内部步骤，正式消费
    out.push(item.event);
  }
  return out;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((v, i) => deepEqual(v, (b as unknown[])[i]));
  }
  if (typeof a === 'object') {
    const ka = Object.keys(a as object);
    const kb = Object.keys(b as object);
    if (ka.length !== kb.length) return false;
    return ka.every(
      (k) =>
        Object.prototype.hasOwnProperty.call(b, k) &&
        deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
    );
  }
  return false;
}

/**
 * 比较两条轨迹，返回首次语义分歧（若有）。
 *
 * @param traceA 执行器 A 的轨迹（可含循环压缩段）
 * @param traceB 执行器 B 的轨迹
 * @param spec   输入给定的可观察动作定义
 */
export function diffTraces(
  traceA: TraceEvent[],
  traceB: TraceEvent[],
  spec: ObservableSpec,
  options: DiffOptions = {},
): DiffReport {
  const windowSize = options.windowSize ?? DEFAULT_WINDOW_SIZE;
  const lookaheadLimit = options.lookaheadLimit ?? DEFAULT_LOOKAHEAD_LIMIT;
  const isObservable = buildObservableMatcher(spec);

  const a: SideState = { cursor: new TraceCursor(traceA, 'A'), before: new RingBuffer(windowSize), observableCount: 0 };
  const b: SideState = { cursor: new TraceCursor(traceB, 'B'), before: new RingBuffer(windowSize), observableCount: 0 };

  let divergence: Divergence | undefined;

  for (;;) {
    const obsA = nextObservable(a, isObservable);
    const obsB = nextObservable(b, isObservable);

    if (obsA === null && obsB === null) break; // 两侧同时耗尽：语义一致

    if (obsA === null || obsB === null) {
      // 一侧耗尽：另一侧的可观察动作即为缺失动作
      const present = obsA ?? obsB!;
      divergence = {
        kind: 'missing-action',
        observableIndex: present.observableIndex,
        a: obsA ?? undefined,
        b: obsB ?? undefined,
        message: obsA === null
          ? `A 侧缺失可观察动作 "${present.action}"（B 侧位于 ${present.path}）`
          : `B 侧缺失可观察动作 "${present.action}"（A 侧位于 ${present.path}）`,
      };
      break;
    }

    if (obsA.action === obsB.action) {
      if (!deepEqual(obsA.value, obsB.value)) {
        divergence = {
          kind: 'value-mismatch',
          observableIndex: obsA.observableIndex,
          a: obsA,
          b: obsB,
          message:
            `可观察动作 "${obsA.action}" 的值不一致：` +
            `A=${JSON.stringify(obsA.value)}（${obsA.path}），` +
            `B=${JSON.stringify(obsB.value)}（${obsB.path}）`,
        };
        break;
      }
      continue; // 对齐成功，推进下一对
    }

    // 动作名不同：通过有界前瞻区分「乱序」与「缺失」
    const xInB = lookahead(b, isObservable, obsA.action, lookaheadLimit);
    const yInA = lookahead(a, isObservable, obsB.action, lookaheadLimit);

    if (xInB.found || yInA.found) {
      const hit = xInB.found ? xInB : yInA;
      divergence = {
        kind: 'out-of-order',
        observableIndex: obsA.observableIndex,
        a: obsA,
        b: obsB,
        lookahead: { foundAtOffset: hit.foundAtOffset, truncated: hit.truncated },
        message:
          `可观察动作顺序不一致：A 侧第 ${obsA.observableIndex} 个为 "${obsA.action}"，` +
          `B 侧为 "${obsB.action}"；` +
          (xInB.found
            ? `"${obsA.action}" 在 B 侧其后第 ${xInB.foundAtOffset} 个可观察动作处出现`
            : `"${obsB.action}" 在 A 侧其后第 ${yInA.foundAtOffset} 个可观察动作处出现`),
      };
    } else {
      const truncated = xInB.truncated || yInA.truncated;
      divergence = {
        kind: 'missing-action',
        observableIndex: obsA.observableIndex,
        a: obsA,
        b: obsB,
        lookahead: truncated ? { foundAtOffset: -1, truncated: true } : undefined,
        message:
          `B 侧缺失可观察动作 "${obsA.action}"（A 侧位于 ${obsA.path}），` +
          `且 A 侧缺失 "${obsB.action}"（B 侧位于 ${obsB.path}）` +
          (truncated ? `（前瞻达到上限 ${lookaheadLimit}，结论基于有界扫描）` : ''),
      };
    }
    break;
  }

  return {
    equal: divergence === undefined,
    divergence,
    window: {
      a: { before: a.before.toArray(), after: collectAfter(a, isObservable, windowSize) },
      b: { before: b.before.toArray(), after: collectAfter(b, isObservable, windowSize) },
    },
    observedCounts: { a: a.observableCount, b: b.observableCount },
  };
}
