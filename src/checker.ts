/**
 * 独立模型检查器：与执行器分离的一步语义实现。
 * - 用有限输入域（数据域 + 事件负载域）做 BFS 穷举。
 * - 不变量违反返回最短反例轨迹；不可达状态给出证明或最短到达轨迹。
 * - step() 供执行器轨迹交叉核验。
 */

import {
  compile,
  enterPath,
  initialConfig,
  stableStringify,
  type CompiledSpec,
  type Config,
  type Data,
  type Ev,
  type Spec,
  type Transition,
} from "./spec.ts";

export interface StepResult {
  next: Config | null;
  reason: "ok" | "no-transition" | "guard-failed" | "invariant-violated";
}

/** 独立的一步语义：不依赖 executor.ts 的任何函数。 */
export function step(compiled: CompiledSpec, config: Config, event: Ev): StepResult {
  const candidates = compiled.byEvent.get(event.type) ?? [];
  let chosen: Transition | null = null;
  let chosenDepth = -1;
  for (const t of candidates) {
    const depth = t.from === undefined ? 0 : config.path.indexOf(t.from) + 1;
    if (t.from !== undefined && depth === 0) continue;
    if (depth > chosenDepth) {
      chosen = t;
      chosenDepth = depth;
    }
  }
  if (!chosen) return { next: null, reason: "no-transition" };

  const payload: Data = event.payload ?? {};
  if (chosen.guard && !chosen.guard(config.data, payload)) {
    return { next: null, reason: "guard-failed" };
  }

  const data: Data = { ...config.data };
  if (chosen.action) {
    const patch = chosen.action(config.data, payload);
    for (const key of Object.keys(patch)) data[key] = patch[key]!;
  }
  const path = chosen.to === undefined ? [...config.path] : enterPath(compiled, chosen.to);
  const next: Config = { path, data };

  for (const inv of compiled.spec.invariants) {
    if (!inv.check(next.data, next.path)) {
      return { next: null, reason: "invariant-violated" };
    }
  }
  return { next, reason: "ok" };
}

function keyOf(config: Config): string {
  return stableStringify(config);
}

/** 有限输入域约束：数据字段超出声明域的后继不计入建模空间。 */
function withinDomains(spec: Spec, config: Config): boolean {
  const domains = spec.domains;
  if (!domains) return true;
  for (const [field, values] of Object.entries(domains)) {
    const value = config.data[field];
    if (value !== undefined && !values.includes(value)) return false;
  }
  return true;
}

/** 枚举一个事件在有限负载域下的所有实例。 */
function eventInstances(spec: Spec): Ev[] {
  const types = new Set<string>();
  for (const t of spec.transitions) types.add(t.event);
  const out: Ev[] = [];
  for (const type of [...types].sort()) {
    const payloads = spec.eventDomains?.[type] ?? [{}];
    for (const payload of payloads) {
      out.push({ type, payload });
    }
  }
  return out;
}

export interface TraceStep {
  event: Ev;
  config: Config;
}

export interface ExploreResult {
  states: Map<string, Config>;
  /** 每个状态的最短到达轨迹（BFS 保证最短）。 */
  traces: Map<string, TraceStep[]>;
  visitedStateIds: Set<string>;
  truncated: boolean;
}

export function explore(spec: Spec, options: { maxStates?: number; maxDepth?: number } = {}): ExploreResult {
  const compiled = compile(spec);
  const maxStates = options.maxStates ?? 100_000;
  const maxDepth = options.maxDepth ?? 64;
  const events = eventInstances(spec);

  const start = initialConfig(compiled);
  const states = new Map<string, Config>();
  const traces = new Map<string, TraceStep[]>();
  const visitedStateIds = new Set<string>();
  let truncated = false;

  const startKey = keyOf(start);
  states.set(startKey, start);
  traces.set(startKey, []);
  let frontier: Config[] = [start];

  for (let depth = 0; depth < maxDepth && frontier.length > 0; depth++) {
    const nextFrontier: Config[] = [];
    for (const config of frontier) {
      for (const event of events) {
        const result = step(compiled, config, event);
        if (!result.next) continue;
        if (!withinDomains(spec, result.next)) continue;
        const key = keyOf(result.next);
        if (states.has(key)) continue;
        if (states.size >= maxStates) {
          truncated = true;
          break;
        }
        states.set(key, result.next);
        const parentTrace = traces.get(keyOf(config))!;
        traces.set(key, [...parentTrace, { event, config: result.next }]);
        for (const id of result.next.path) visitedStateIds.add(id);
        nextFrontier.push(result.next);
      }
      if (truncated) break;
    }
    if (truncated) break;
    frontier = nextFrontier;
  }
  for (const id of start.path) visitedStateIds.add(id);
  return { states, traces, visitedStateIds, truncated };
}

export interface Counterexample {
  invariant: string;
  trace: TraceStep[];
  finalConfig: Config;
}

/**
 * 原始一步语义（不做不变量过滤）：用于发现“规约允许到达但违反不变量”的反例。
 * 执行器会在运行时拦截这些状态；检查器则证明它们是否本可到达。
 */
export function rawStep(compiled: CompiledSpec, config: Config, event: Ev): Config | null {
  const candidates = compiled.byEvent.get(event.type) ?? [];
  let chosen: Transition | null = null;
  let chosenDepth = -1;
  for (const t of candidates) {
    const depth = t.from === undefined ? 0 : config.path.indexOf(t.from) + 1;
    if (t.from !== undefined && depth === 0) continue;
    if (depth > chosenDepth) {
      chosen = t;
      chosenDepth = depth;
    }
  }
  if (!chosen) return null;
  const payload: Data = event.payload ?? {};
  if (chosen.guard && !chosen.guard(config.data, payload)) return null;
  const data: Data = { ...config.data };
  if (chosen.action) {
    const patch = chosen.action(config.data, payload);
    for (const key of Object.keys(patch)) data[key] = patch[key]!;
  }
  const path = chosen.to === undefined ? [...config.path] : enterPath(compiled, chosen.to);
  return { path, data };
}

/** 验证不变量：BFS 找到的第一个违反即最短反例；全部满足返回 null。 */
export function findInvariantViolation(
  spec: Spec,
  options: { maxStates?: number; maxDepth?: number } = {},
): Counterexample | null {
  const compiled = compile(spec);
  const events = eventInstances(spec);
  const maxStates = options.maxStates ?? 100_000;
  const maxDepth = options.maxDepth ?? 64;

  const start = initialConfig(compiled);
  const seen = new Set<string>([keyOf(start)]);
  let frontier: { config: Config; trace: TraceStep[] }[] = [{ config: start, trace: [] }];

  const violates = (config: Config): string | null => {
    for (const inv of spec.invariants) {
      if (!inv.check(config.data, config.path)) return inv.name;
    }
    return null;
  };
  const atStart = violates(start);
  if (atStart) return { invariant: atStart, trace: [], finalConfig: start };

  for (let depth = 0; depth < maxDepth && frontier.length > 0; depth++) {
    const nextFrontier: { config: Config; trace: TraceStep[] }[] = [];
    for (const { config, trace } of frontier) {
      for (const event of events) {
        const next = rawStep(compiled, config, event);
        if (!next) continue;
        if (!withinDomains(spec, next)) continue;
        const key = keyOf(next);
        if (seen.has(key)) continue;
        if (seen.size >= maxStates) return null;
        seen.add(key);
        const nextTrace = [...trace, { event, config: next }];
        const bad = violates(next);
        if (bad) return { invariant: bad, trace: nextTrace, finalConfig: next };
        nextFrontier.push({ config: next, trace: nextTrace });
      }
    }
    frontier = nextFrontier;
  }
  return null;
}

export interface UnreachableReport {
  unreachable: string[];
  truncated: boolean;
}

/** 不可达状态：在有限域穷举下从未进入的声明状态。 */
export function findUnreachableStates(
  spec: Spec,
  options: { maxStates?: number; maxDepth?: number } = {},
): UnreachableReport {
  const result = explore(spec, options);
  const unreachable = spec.states
    .map((s) => s.id)
    .filter((id) => !result.visitedStateIds.has(id));
  return { unreachable, truncated: result.truncated };
}

/**
 * 证明某状态不可达；若可达则返回最短到达轨迹（不可达性声明的反例）。
 */
export function proveUnreachable(
  spec: Spec,
  stateId: string,
  options: { maxStates?: number; maxDepth?: number } = {},
): { unreachable: boolean; trace: TraceStep[] | null } {
  const compiled = compile(spec);
  const events = eventInstances(spec);
  const maxStates = options.maxStates ?? 100_000;
  const maxDepth = options.maxDepth ?? 64;

  const start = initialConfig(compiled);
  if (start.path.includes(stateId)) return { unreachable: false, trace: [] };
  const seen = new Set<string>([keyOf(start)]);
  let frontier: { config: Config; trace: TraceStep[] }[] = [{ config: start, trace: [] }];

  for (let depth = 0; depth < maxDepth && frontier.length > 0; depth++) {
    const nextFrontier: { config: Config; trace: TraceStep[] }[] = [];
    for (const { config, trace } of frontier) {
      for (const event of events) {
        const next = rawStep(compiled, config, event);
        if (!next) continue;
        if (!withinDomains(spec, next)) continue;
        const key = keyOf(next);
        if (seen.has(key)) continue;
        if (seen.size >= maxStates) return { unreachable: false, trace: null };
        seen.add(key);
        const nextTrace = [...trace, { event, config: next }];
        if (next.path.includes(stateId)) return { unreachable: false, trace: nextTrace };
        nextFrontier.push({ config: next, trace: nextTrace });
      }
    }
    frontier = nextFrontier;
  }
  return { unreachable: true, trace: null };
}
