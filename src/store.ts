/**
 * 事务性键值存储 + 跨字段不变量引擎。
 *
 * 核心保证：
 * - 提交前只验证受影响的不变量（按草稿上的实际依赖索引）。
 * - 不变量依赖可随条件分支变化：每次提交先在事务草稿上重算实际依赖，再验证。
 * - 多条不变量可共享纯计算（按草稿记忆化，一次求值多方复用）。
 * - 提交失败返回涉及字段、最小相关操作子集与可重放反例。
 * - 拒绝的提交不留新依赖边、不留半更新状态（草稿与暂存依赖图整体丢弃）。
 * - 小事务在增量验证之外逐条全验对照。
 * - 读者只能看到已提交快照，永远看不到事务中间态。
 * - 状态操作（Op）可序列化、可回放。
 */

export type FieldName = string;

/** 唯一的状态操作类型：设置字段。可序列化、可回放。 */
export interface SetOp {
  readonly kind: "set";
  readonly field: FieldName;
  readonly value: unknown;
}
export type Op = SetOp;

export type State = Readonly<Record<FieldName, unknown>>;

/** 不变量违例。规则抛出任何错误（含非 Violation）都会使提交失败。 */
export class Violation extends Error {
  constructor(message: string) {
    super(message);
    this.name = "Violation";
  }
}

/** 不变量 / 纯计算读取草稿的上下文。所有读取都会被追踪以推导依赖。 */
export interface ReadCtx {
  get(field: FieldName): unknown;
  /** 共享纯计算：同一草稿内同名计算只求值一次。 */
  compute<T>(unit: ComputeUnit<T>): T;
}

export interface ComputeUnit<T> {
  readonly name: string;
  readonly run: (ctx: ReadCtx) => T;
}

export function defineCompute<T>(
  name: string,
  run: (ctx: ReadCtx) => T,
): ComputeUnit<T> {
  return { name, run };
}

export interface Invariant {
  readonly name: string;
  /**
   * 廉价的依赖探测：在草稿上做被追踪的读取，得到该规则当前的实际依赖集。
   * 允许条件分支——读取了哪些字段，哪些就是依赖。
   */
  readonly deps: (ctx: ReadCtx) => void;
  /** 昂贵的正式验证。抛出 Violation 或任何错误都会拒绝本次提交。 */
  readonly check: (ctx: ReadCtx) => void;
}

export interface Counterexample {
  readonly baseVersion: number;
  readonly ops: readonly Op[];
}

export type FailureKind = "violation" | "rule-error";

export interface RuleFailure {
  readonly rule: string;
  readonly kind: FailureKind;
  readonly message: string;
  /** 该规则在本草稿上的实际依赖字段（即“涉及字段”）。 */
  readonly fields: readonly FieldName[];
  /** 最小相关操作子集：只保留触及涉及字段的操作。 */
  readonly relevantOps: readonly Op[];
  /** 可重放反例：baseVersion 快照 + relevantOps。 */
  readonly counterexample: Counterexample;
  /** incremental = 增量阶段发现；exhaustive = 小事务全验对照阶段补获。 */
  readonly detectedBy: "incremental" | "exhaustive";
}

export type CommitResult =
  | {
      readonly ok: true;
      readonly version: number;
      readonly verified: "incremental" | "exhaustive";
      readonly checkedRules: readonly string[];
    }
  | { readonly ok: false; readonly failures: readonly RuleFailure[] };

export interface StoreOptions {
  /** 操作数不超过该阈值的事务，除增量验证外再逐条全验对照。 */
  readonly smallTxThreshold?: number;
}

interface StoreStats {
  readonly checkInvocations: Map<string, number>;
  readonly computeEvaluations: Map<string, number>;
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const key of Object.keys(value as Record<string, unknown>)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
    Object.freeze(value);
  }
  return value;
}

function intersects(a: ReadonlySet<string>, b: ReadonlySet<string>): boolean {
  for (const x of a) if (b.has(x)) return true;
  return false;
}

class TrackedCtx implements ReadCtx {
  readonly reads = new Set<FieldName>();
  private readonly state: Record<FieldName, unknown>;
  private readonly memo: Map<string, unknown>;
  private readonly stats: StoreStats;

  constructor(
    state: Record<FieldName, unknown>,
    memo: Map<string, unknown>,
    stats: StoreStats,
  ) {
    this.state = state;
    this.memo = memo;
    this.stats = stats;
  }

  get(field: FieldName): unknown {
    this.reads.add(field);
    return this.state[field];
  }

  compute<T>(unit: ComputeUnit<T>): T {
    if (this.memo.has(unit.name)) {
      return this.memo.get(unit.name) as T;
    }
    this.stats.computeEvaluations.set(
      unit.name,
      (this.stats.computeEvaluations.get(unit.name) ?? 0) + 1,
    );
    // 计算内部的读取同样被追踪，合并进调用方规则的依赖集。
    const value = unit.run(this);
    this.memo.set(unit.name, value);
    return value;
  }
}

export class Transaction {
  private readonly ops: SetOp[] = [];
  private readonly store: Store;

  constructor(store: Store) {
    this.store = store;
  }

  set(field: FieldName, value: unknown): this {
    this.ops.push({ kind: "set", field, value });
    return this;
  }

  /** 已记录的操作日志，可序列化、可回放。 */
  get recordedOps(): readonly Op[] {
    return this.ops;
  }

  commit(): CommitResult {
    return this.store.commit(this.ops);
  }
}

export class Store {
  private committed: Record<FieldName, unknown>;
  private version = 0;
  private readonly snapshots = new Map<number, Record<FieldName, unknown>>();
  private readonly invariants = new Map<string, Invariant>();
  /** 已提交的依赖图：规则名 -> 字段集。只在提交成功时整体替换。 */
  private depGraph = new Map<string, ReadonlySet<FieldName>>();
  private readonly everChecked = new Set<string>();
  private readonly smallTxThreshold: number;
  private readonly stats: StoreStats = {
    checkInvocations: new Map(),
    computeEvaluations: new Map(),
  };

  constructor(initial: Record<FieldName, unknown>, options: StoreOptions = {}) {
    this.committed = deepFreeze({ ...initial });
    this.snapshots.set(0, this.committed);
    this.smallTxThreshold = options.smallTxThreshold ?? 8;
  }

  /** 读者只能拿到已提交快照（冻结对象），永远看不到事务中间态。 */
  read(): State {
    return this.committed;
  }

  get currentVersion(): number {
    return this.version;
  }

  /** 观测钩子：每条规则 check 的实际执行次数（验证“不相关字段不触发昂贵规则”）。 */
  get checkInvocations(): ReadonlyMap<string, number> {
    return this.stats.checkInvocations;
  }

  /** 观测钩子：每个共享纯计算的实际求值次数（验证“共享纯计算只求值一次”）。 */
  get computeEvaluations(): ReadonlyMap<string, number> {
    return this.stats.computeEvaluations;
  }

  /** 当前已提交的依赖图（用于验证“拒绝操作不留新依赖边”）。 */
  dependencyEdges(): ReadonlyMap<string, ReadonlySet<FieldName>> {
    return this.depGraph;
  }

  registerInvariant(inv: Invariant): void {
    if (this.invariants.has(inv.name)) {
      throw new Error(`invariant already registered: ${inv.name}`);
    }
    this.invariants.set(inv.name, inv);
    // 注册时基于已提交状态推导初始依赖边。
    const ctx = new TrackedCtx(this.committed, new Map(), this.stats);
    inv.deps(ctx);
    this.depGraph.set(inv.name, ctx.reads);
  }

  begin(): Transaction {
    return new Transaction(this);
  }

  commit(ops: readonly Op[]): CommitResult {
    // 1. 在私有草稿上顺序应用操作；读者不可见。
    const draft: Record<FieldName, unknown> = { ...this.committed };
    const changed = new Set<FieldName>();
    for (const op of ops) {
      draft[op.field] = op.value;
      changed.add(op.field);
    }

    // 本次提交共享的纯计算记忆表：多条不变量复用同一次求值。
    const memo = new Map<string, unknown>();

    // 2. 先在草稿上重算每条规则的实际依赖（暂存，未提交）。
    const stagedDeps = new Map<string, ReadonlySet<FieldName>>();
    for (const inv of this.invariants.values()) {
      const ctx = new TrackedCtx(draft, memo, this.stats);
      inv.deps(ctx);
      stagedDeps.set(inv.name, ctx.reads);
    }

    // 3. 受影响规则 = 新旧依赖与变更字段有交集，或从未验证过。
    const affected: Invariant[] = [];
    for (const inv of this.invariants.values()) {
      const oldDeps = this.depGraph.get(inv.name) ?? new Set<FieldName>();
      const newDeps = stagedDeps.get(inv.name)!;
      if (
        !this.everChecked.has(inv.name) ||
        intersects(oldDeps, changed) ||
        intersects(newDeps, changed)
      ) {
        affected.push(inv);
      }
    }

    // 4. 只验证受影响规则。规则抛出任何错误都视为提交失败。
    const failures: RuleFailure[] = [];
    const checkedRules: string[] = [];
    const runCheck = (inv: Invariant, detectedBy: "incremental" | "exhaustive") => {
      this.stats.checkInvocations.set(
        inv.name,
        (this.stats.checkInvocations.get(inv.name) ?? 0) + 1,
      );
      try {
        inv.check(new TrackedCtx(draft, memo, this.stats));
      } catch (err) {
        const fields = [...(stagedDeps.get(inv.name) ?? [])].sort();
        const relevantOps = ops.filter((op) => fields.includes(op.field));
        failures.push({
          rule: inv.name,
          kind: err instanceof Violation ? "violation" : "rule-error",
          message: err instanceof Error ? err.message : String(err),
          fields,
          relevantOps,
          counterexample: { baseVersion: this.version, ops: relevantOps },
          detectedBy,
        });
      }
    };
    for (const inv of affected) {
      checkedRules.push(inv.name);
      runCheck(inv, "incremental");
    }

    // 5. 小事务：逐条全验对照，防止依赖声明遗漏导致漏检。
    let verified: "incremental" | "exhaustive" = "incremental";
    if (ops.length <= this.smallTxThreshold) {
      verified = "exhaustive";
      for (const inv of this.invariants.values()) {
        if (!affected.includes(inv)) {
          checkedRules.push(inv.name);
          runCheck(inv, "exhaustive");
        }
      }
    }

    // 6. 失败：草稿与暂存依赖图整体丢弃——不留新依赖边、不留半更新状态。
    if (failures.length > 0) {
      return { ok: false, failures };
    }

    // 7. 成功：原子换出快照与依赖图。
    this.committed = deepFreeze(draft);
    this.version += 1;
    this.snapshots.set(this.version, this.committed);
    this.depGraph = stagedDeps;
    for (const name of checkedRules) this.everChecked.add(name);
    return { ok: true, version: this.version, verified, checkedRules };
  }

  /** 回放一条操作日志（例如反例），返回重放结果。 */
  replay(ops: readonly Op[]): CommitResult {
    return this.commit(ops);
  }

  /** 重放反例：从 baseVersion 快照出发，在临时副本上应用相同规则与操作。 */
  replayCounterexample(ce: Counterexample): CommitResult {
    const base = this.snapshots.get(ce.baseVersion);
    if (!base) {
      throw new Error(`unknown base version: ${ce.baseVersion}`);
    }
    const replica = new Store({ ...base }, {
      smallTxThreshold: this.smallTxThreshold,
    });
    for (const inv of this.invariants.values()) replica.registerInvariant(inv);
    return replica.commit(ce.ops);
  }
}
