/**
 * 原子命令存储：
 * - 命令 = 执行 + 逆操作 + 前置版本
 * - 多命令组合为一次原子提交（暂存区执行，提交瞬间才对读者可见）
 * - 中途失败按反向顺序回滚已执行部分，并校验回滚后状态与提交前一致
 * - 撤销按反向顺序应用逆操作；版本不匹配时报冲突而非误改
 * - 每个分支拥有独立历史，切换/撤销不影响未选分支
 * - 全部操作记录在只追加日志中，可回放重建状态
 */

export interface AppliedCommand<S> {
  readonly name: string;
  /** 执行后的状态 */
  readonly state: S;
  /** 逆操作：把（当前的）状态恢复为执行前状态 */
  readonly undo: (state: S) => S;
}

export interface Command<S> {
  readonly name: string;
  /** 前置版本：命令只能应用在恰好处于该版本的状态上 */
  readonly expectedVersion?: number;
  execute(state: S): AppliedCommand<S>;
}

interface CommitRecord<S> {
  readonly id: number;
  readonly fromVersion: number;
  readonly toVersion: number;
  readonly fromState: S; // 冻结的提交前快照，用于撤销完整性校验
  readonly commands: readonly Command<S>[]; // 正操作，供回放
  readonly applied: readonly AppliedCommand<S>[]; // 逆操作，供回滚/撤销
}

type LogEvent<S> =
  | { readonly type: "commit"; readonly record: CommitRecord<S> }
  | { readonly type: "undo"; readonly record: CommitRecord<S> };

interface Branch<S> {
  readonly name: string;
  state: S;
  version: number;
  readonly baseState: S; // 分支起点状态（回放起点）
  readonly baseVersion: number;
  readonly history: CommitRecord<S>[];
  readonly log: LogEvent<S>[];
}

export class PreconditionFailedError extends Error {
  readonly commandName: string;
  readonly expected: number;
  readonly actual: number;
  constructor(commandName: string, expected: number, actual: number) {
    super(
      `前置版本不满足：命令 "${commandName}" 期望版本 ${expected}，实际版本 ${actual}`,
    );
    this.name = "PreconditionFailedError";
    this.commandName = commandName;
    this.expected = expected;
    this.actual = actual;
  }
}

export class TransactionRolledBackError extends Error {
  readonly failedCommand: string;
  readonly rolledBackCount: number;
  constructor(failedCommand: string, rolledBackCount: number, cause: unknown) {
    super(
      `事务已回滚：命令 "${failedCommand}" 失败，已反向回滚 ${rolledBackCount} 个已执行命令`,
    );
    this.name = "TransactionRolledBackError";
    this.failedCommand = failedCommand;
    this.rolledBackCount = rolledBackCount;
    this.cause = cause;
  }
}

export class ConflictError extends Error {
  readonly expected: number;
  readonly actual: number;
  constructor(expected: number, actual: number) {
    super(
      `撤销冲突：当前版本 ${actual} 与待撤销提交的版本 ${expected} 不一致，拒绝误改`,
    );
    this.name = "ConflictError";
    this.expected = expected;
    this.actual = actual;
  }
}

export class InverseIncompleteError extends Error {
  constructor(context: string) {
    super(`逆操作不完整：${context} 后状态未恢复原样`);
    this.name = "InverseIncompleteError";
  }
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const key of Object.keys(value)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
    Object.freeze(value);
  }
  return value;
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (
    a === null || b === null ||
    typeof a !== "object" || typeof b !== "object"
  ) {
    return false;
  }
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  return ka.every((k) =>
    deepEqual(
      (a as Record<string, unknown>)[k],
      (b as Record<string, unknown>)[k],
    )
  );
}

export class Store<S> {
  private readonly branches = new Map<string, Branch<S>>();
  private current: string;
  private nextCommitId = 1;

  constructor(initialState: S, mainBranch = "main") {
    const frozen = deepFreeze(structuredClone(initialState));
    this.branches.set(mainBranch, {
      name: mainBranch,
      state: frozen,
      version: 0,
      baseState: frozen,
      baseVersion: 0,
      history: [],
      log: [],
    });
    this.current = mainBranch;
  }

  private branch(): Branch<S> {
    return this.branches.get(this.current)!;
  }

  get currentBranch(): string {
    return this.current;
  }

  get version(): number {
    return this.branch().version;
  }

  /** 读者只能拿到冻结快照；事务提交前永远看到旧状态 */
  read(): S {
    return this.branch().state;
  }

  /** 从当前分支派生新分支；新分支历史独立，互不影响 */
  createBranch(name: string): void {
    if (this.branches.has(name)) {
      throw new Error(`分支已存在: ${name}`);
    }
    const from = this.branch();
    this.branches.set(name, {
      name,
      state: from.state,
      version: from.version,
      baseState: from.state,
      baseVersion: from.version,
      history: [],
      log: [],
    });
  }

  checkout(name: string): void {
    if (!this.branches.has(name)) {
      throw new Error(`分支不存在: ${name}`);
    }
    this.current = name;
  }

  branchNames(): string[] {
    return [...this.branches.keys()];
  }

  historySize(branch?: string): number {
    return this.branches.get(branch ?? this.current)!.history.length;
  }

  /** 原子提交：全部成功才可见；任一失败则反向回滚已执行部分 */
  commit(commands: readonly Command<S>[]): CommitRecord<S> {
    const branch = this.branch();
    const fromVersion = branch.version;
    const fromState = branch.state;
    let staging = structuredClone(fromState);
    let stagedVersion = fromVersion;
    const applied: AppliedCommand<S>[] = [];

    const rollback = () => {
      for (let i = applied.length - 1; i >= 0; i--) {
        staging = applied[i].undo(staging);
      }
      if (!deepEqual(staging, fromState)) {
        throw new InverseIncompleteError("事务回滚");
      }
    };

    for (const command of commands) {
      if (
        command.expectedVersion !== undefined &&
        command.expectedVersion !== stagedVersion
      ) {
        rollback();
        throw new PreconditionFailedError(
          command.name,
          command.expectedVersion,
          stagedVersion,
        );
      }
      let result: AppliedCommand<S>;
      try {
        result = command.execute(staging);
      } catch (cause) {
        rollback();
        throw new TransactionRolledBackError(
          command.name,
          applied.length,
          cause,
        );
      }
      applied.push(result);
      staging = result.state;
      stagedVersion += 1;
    }

    // 提交瞬间才切换可见状态，读者看不到中间态
    const record: CommitRecord<S> = {
      id: this.nextCommitId++,
      fromVersion,
      toVersion: stagedVersion,
      fromState,
      commands: [...commands],
      applied: [...applied],
    };
    branch.state = deepFreeze(staging);
    branch.version = stagedVersion;
    branch.history.push(record);
    branch.log.push({ type: "commit", record });
    return record;
  }

  /** 撤销最近一次提交：反向应用全部逆操作；版本不符则冲突 */
  undo(): CommitRecord<S> {
    const branch = this.branch();
    const record = branch.history.pop();
    if (!record) {
      throw new Error("没有可撤销的提交");
    }
    if (branch.version !== record.toVersion) {
      branch.history.push(record);
      throw new ConflictError(record.toVersion, branch.version);
    }
    let staging = structuredClone(branch.state);
    for (let i = record.applied.length - 1; i >= 0; i--) {
      staging = record.applied[i].undo(staging);
    }
    if (!deepEqual(staging, record.fromState)) {
      throw new InverseIncompleteError(`撤销提交 #${record.id}`);
    }
    branch.state = deepFreeze(staging);
    branch.version = record.fromVersion;
    branch.log.push({ type: "undo", record });
    return record;
  }

  /** 管理操作：加载外部快照（模拟并发写/快照恢复），不改历史 */
  restore(state: S, version: number): void {
    const branch = this.branch();
    branch.state = deepFreeze(structuredClone(state));
    branch.version = version;
  }

  /** 从分支起点回放日志，重建当前状态（可回放性验证） */
  replay(name = this.current): { state: S; version: number } {
    const branch = this.branches.get(name);
    if (!branch) throw new Error(`分支不存在: ${name}`);
    let state = structuredClone(branch.baseState);
    let version = branch.baseVersion;
    for (const event of branch.log) {
      if (event.type === "commit") {
        for (const command of event.record.commands) {
          state = command.execute(state).state;
          version += 1;
        }
      } else {
        for (let i = event.record.applied.length - 1; i >= 0; i--) {
          state = event.record.applied[i].undo(state);
        }
        version = event.record.fromVersion;
      }
    }
    return { state, version };
  }
}
