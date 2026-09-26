/**
 * 交互执行器：由规约生成。
 * - 不变量检查：每次迁移后校验，违反则整体回滚。
 * - 非法事件：不改状态，保留诊断。
 * - 事件录制与回放：相同初态 + 相同事件序列 => 相同结果。
 * - 副作用回调：按事务约定，任一失败则回滚状态。
 * - 热换规约：需要迁移证据，失败保留旧规约与状态。
 */

import { createHash } from "node:crypto";
import {
  compile,
  configEquals,
  enterPath,
  initialConfig,
  stableStringify,
  validatePath,
  type CompiledSpec,
  type Config,
  type Data,
  type Ev,
  type Spec,
  type Transition,
} from "./spec.ts";

export type Status = "applied" | "rejected" | "rolled-back";

export interface Diagnostic {
  code:
    | "no-transition"
    | "guard-failed"
    | "invariant-violated"
    | "effect-failed"
    | "missing-target";
  message: string;
}

export interface JournalEntry {
  seq: number;
  event: Ev;
  status: Status;
  diagnostics: Diagnostic[];
  beforeHash: string;
  afterHash: string;
}

export interface DispatchResult {
  status: Status;
  diagnostics: Diagnostic[];
  config: Config;
}

export type EffectFn = (config: Config, event: Ev) => void;

export interface NodeTreeLike {
  has(id: string): boolean;
}

export function hashConfig(config: Config): string {
  return createHash("sha256").update(stableStringify(config)).digest("hex").slice(0, 16);
}

function cloneConfig(config: Config): Config {
  return { path: [...config.path], data: { ...config.data } };
}

export class Executor {
  private compiled: CompiledSpec;
  private config: Config;
  private effects = new Map<string, EffectFn>();
  private nodeTree: NodeTreeLike | null = null;
  readonly journal: JournalEntry[] = [];

  constructor(spec: Spec) {
    this.compiled = compile(spec);
    this.config = initialConfig(this.compiled);
  }

  get spec(): Spec {
    return this.compiled.spec;
  }

  getConfig(): Config {
    return cloneConfig(this.config);
  }

  get hash(): string {
    return hashConfig(this.config);
  }

  registerEffect(name: string, fn: EffectFn): void {
    this.effects.set(name, fn);
  }

  attachNodeTree(tree: NodeTreeLike): void {
    this.nodeTree = tree;
  }

  /** 选择迁移：事件匹配 + from 在活动路径中，取最深（最具体）者。 */
  private selectTransition(event: Ev): { t: Transition } | { t: null } {
    const candidates = this.compiled.byEvent.get(event.type) ?? [];
    let best: Transition | null = null;
    let bestDepth = -1;
    for (const t of candidates) {
      if (t.from === undefined) {
        if (bestDepth < 0) {
          best = t;
          bestDepth = 0;
        }
        continue;
      }
      const idx = this.config.path.indexOf(t.from);
      if (idx >= 0 && idx + 1 > bestDepth) {
        best = t;
        bestDepth = idx + 1;
      }
    }
    return { t: best };
  }

  private checkInvariants(config: Config): Diagnostic[] {
    const out: Diagnostic[] = [];
    for (const inv of this.compiled.spec.invariants) {
      if (!inv.check(config.data, config.path)) {
        out.push({
          code: "invariant-violated",
          message: `invariant "${inv.name}" violated`,
        });
      }
    }
    return out;
  }

  dispatch(event: Ev): DispatchResult {
    const before = cloneConfig(this.config);
    const beforeHash = hashConfig(before);
    const diagnostics: Diagnostic[] = [];
    let status: Status = "applied";

    // 回放/分发时若事件带目标节点而节点树中不存在 => 报分歧，不改状态。
    if (event.target !== undefined && this.nodeTree && !this.nodeTree.has(event.target)) {
      diagnostics.push({
        code: "missing-target",
        message: `target node "${event.target}" not found in node tree`,
      });
      status = "rejected";
      this.record(event, status, diagnostics, beforeHash, beforeHash);
      return { status, diagnostics, config: cloneConfig(this.config) };
    }

    const { t } = this.selectTransition(event);
    if (!t) {
      diagnostics.push({
        code: "no-transition",
        message: `no transition for event "${event.type}" in states [${this.config.path.join(",")}]`,
      });
      status = "rejected";
      this.record(event, status, diagnostics, beforeHash, beforeHash);
      return { status, diagnostics, config: cloneConfig(this.config) };
    }

    const payload: Data = event.payload ?? {};
    if (t.guard && !t.guard(this.config.data, payload)) {
      diagnostics.push({
        code: "guard-failed",
        message: `guard rejected event "${event.type}"`,
      });
      status = "rejected";
      this.record(event, status, diagnostics, beforeHash, beforeHash);
      return { status, diagnostics, config: cloneConfig(this.config) };
    }

    // 原子多字段动作：先算补丁，整体应用到副本，校验通过后一次性提交。
    const next = cloneConfig(this.config);
    if (t.action) {
      const patch = t.action(this.config.data, payload);
      for (const [k, v] of Object.entries(patch)) next.data[k] = v;
    }
    if (t.to !== undefined) next.path = enterPath(this.compiled, t.to);

    const invErrors = this.checkInvariants(next);
    if (invErrors.length > 0) {
      status = "rejected";
      this.record(event, status, invErrors, beforeHash, beforeHash);
      return { status, diagnostics: invErrors, config: cloneConfig(this.config) };
    }

    // 事务约定：状态提交后执行副作用；任一副作用失败 => 回滚状态。
    this.config = next;
    if (t.effects && t.effects.length > 0) {
      for (const name of t.effects) {
        const fn = this.effects.get(name);
        if (!fn) {
          diagnostics.push({
            code: "effect-failed",
            message: `effect "${name}" is not registered`,
          });
          status = "rolled-back";
          break;
        }
        try {
          fn(cloneConfig(this.config), event);
        } catch (err) {
          diagnostics.push({
            code: "effect-failed",
            message: `effect "${name}" threw: ${(err as Error).message}`,
          });
          status = "rolled-back";
          break;
        }
      }
      if (status === "rolled-back") {
        this.config = before;
      }
    }

    this.record(event, status, diagnostics, beforeHash, hashConfig(this.config));
    return { status, diagnostics, config: cloneConfig(this.config) };
  }

  private record(
    event: Ev,
    status: Status,
    diagnostics: Diagnostic[],
    beforeHash: string,
    afterHash: string,
  ): void {
    this.journal.push({
      seq: this.journal.length,
      event,
      status,
      diagnostics,
      beforeHash,
      afterHash,
    });
  }

  /**
   * 热换规约：migrate 产出候选配置作为迁移证据；
   * 证据校验（路径结构 + 新不变量）失败则保留旧规约与状态。
   */
  hotSwap(
    newSpec: Spec,
    migrate: (config: Config) => Config,
  ): { ok: boolean; errors: string[]; evidence?: Config } {
    const compiled = compile(newSpec);
    let candidate: Config;
    try {
      candidate = migrate(cloneConfig(this.config));
    } catch (err) {
      return { ok: false, errors: [`migration threw: ${(err as Error).message}`] };
    }
    const errors = validatePath(compiled, candidate.path);
    for (const inv of newSpec.invariants) {
      if (!inv.check(candidate.data, candidate.path)) {
        errors.push(`invariant "${inv.name}" violated by migrated state`);
      }
    }
    if (errors.length > 0) return { ok: false, errors };
    this.compiled = compiled;
    this.config = { path: [...candidate.path], data: { ...candidate.data } };
    return { ok: true, errors: [], evidence: cloneConfig(candidate) };
  }
}

export interface ReplayDivergence {
  seq: number;
  reason: string;
}

export interface ReplayReport {
  finalHash: string;
  entries: JournalEntry[];
  divergences: ReplayDivergence[];
}

/**
 * 回放：从相同初态重放事件序列，与录制日志逐条比对哈希；
 * 缺目标节点等分歧会被记录。
 */
export function replay(
  spec: Spec,
  events: Ev[],
  options: { recorded?: JournalEntry[]; nodeTree?: NodeTreeLike; effects?: Map<string, EffectFn> } = {},
): ReplayReport {
  const executor = new Executor(spec);
  if (options.nodeTree) executor.attachNodeTree(options.nodeTree);
  if (options.effects) {
    for (const [name, fn] of options.effects) executor.registerEffect(name, fn);
  }
  const divergences: ReplayDivergence[] = [];
  for (const event of events) {
    executor.dispatch(event);
  }
  const entries = executor.journal;
  if (options.recorded) {
    const n = Math.max(entries.length, options.recorded.length);
    for (let i = 0; i < n; i++) {
      const a = options.recorded[i];
      const b = entries[i];
      if (!a || !b) {
        divergences.push({ seq: i, reason: "journal length mismatch" });
        break;
      }
      const missing = b.diagnostics.find((d) => d.code === "missing-target");
      if (missing) {
        divergences.push({ seq: i, reason: missing.message });
        continue;
      }
      if (a.status !== b.status || a.afterHash !== b.afterHash) {
        divergences.push({
          seq: i,
          reason: `status/hash mismatch: recorded ${a.status}@${a.afterHash} vs replayed ${b.status}@${b.afterHash}`,
        });
      }
    }
  } else {
    for (const entry of entries) {
      for (const d of entry.diagnostics) {
        if (d.code === "missing-target") {
          divergences.push({ seq: entry.seq, reason: d.message });
        }
      }
    }
  }
  return { finalHash: executor.hash, entries, divergences };
}

export { configEquals };
