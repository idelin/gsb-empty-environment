import type { Command } from './command.ts';
import { CommandFailedError, VersionConflictError } from './errors.ts';

export interface CommitNode<S> {
  readonly id: string;
  readonly branch: string;
  readonly parentId: string | null;
  readonly commands: readonly Command<S>[];
  readonly baseVersion: number;
  readonly resultVersion: number;
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === 'object') {
    for (const key of Object.keys(value as Record<string, unknown>)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
    Object.freeze(value);
  }
  return value;
}

/**
 * 命令式事务存储。
 * - 提交在私有草稿上执行，提交前读者只能看到旧的已提交快照；
 * - 任一命令失败，已执行部分按反向顺序回滚，整体不产生任何可见影响；
 * - 撤销按逆操作反向重放，且要求当前版本与提交结果版本一致，否则冲突；
 * - 历史是提交节点构成的 DAG，分支各自持有指针，互不覆盖；
 * - 任意分支状态可从初始状态回放命令序列重建。
 */
export class Store<S> {
  private committed: S;
  private version = 0;
  private seq = 0;
  private readonly initial: S;
  private readonly commits = new Map<string, CommitNode<S>>();
  private readonly branches = new Map<string, string | null>();
  private currentBranch = 'main';

  constructor(initial: S) {
    this.initial = structuredClone(initial);
    this.committed = structuredClone(initial);
    this.branches.set('main', null);
  }

  getVersion(): number {
    return this.version;
  }

  getCurrentBranch(): string {
    return this.currentBranch;
  }

  /** 读者快照：已提交状态的冻结副本，事务中间态不可见。 */
  getState(): S {
    return deepFreeze(structuredClone(this.committed));
  }

  /** 原子提交一组命令：单条历史、单次版本递增。 */
  commit(commands: readonly Command<S>[]): CommitNode<S> {
    if (commands.length === 0) throw new Error('commit requires at least one command');
    const baseVersion = this.version;
    const draft = structuredClone(this.committed);
    const done: Command<S>[] = [];
    try {
      for (const cmd of commands) {
        if (cmd.expectedVersion !== undefined && cmd.expectedVersion !== baseVersion) {
          throw new VersionConflictError(
            `command "${cmd.name}" requires version ${cmd.expectedVersion}, current is ${baseVersion}`,
            cmd.expectedVersion,
            baseVersion,
          );
        }
        cmd.execute(draft);
        done.push(cmd);
      }
    } catch (err) {
      const rolledBack: string[] = [];
      for (const cmd of [...done].reverse()) {
        cmd.undo(draft);
        rolledBack.push(cmd.name);
      }
      if (err instanceof VersionConflictError) throw err;
      const failed = commands[done.length];
      throw new CommandFailedError(
        `command "${failed.name}" failed; rolled back ${rolledBack.length} command(s)`,
        failed.name,
        rolledBack,
        err,
      );
    }
    const node: CommitNode<S> = {
      id: `c${++this.seq}`,
      branch: this.currentBranch,
      parentId: this.branches.get(this.currentBranch) ?? null,
      commands: [...commands],
      baseVersion,
      resultVersion: baseVersion + 1,
    };
    this.commits.set(node.id, node);
    this.branches.set(this.currentBranch, node.id);
    this.committed = draft;
    this.version = node.resultVersion;
    return node;
  }

  /** 撤销当前分支顶端提交：逆操作按反向顺序执行。 */
  undo(): CommitNode<S> {
    const tipId = this.branches.get(this.currentBranch);
    if (!tipId) throw new Error('nothing to undo on this branch');
    const node = this.commits.get(tipId)!;
    this.applyInverse(node);
    this.branches.set(this.currentBranch, node.parentId);
    return node;
  }

  /** 撤销指定提交：仅当它是当前分支顶端（版本一致）才允许，否则冲突。 */
  undoCommit(commitId: string): void {
    const node = this.commits.get(commitId);
    if (!node) throw new Error(`unknown commit ${commitId}`);
    this.applyInverse(node);
    if (this.branches.get(this.currentBranch) === commitId) {
      this.branches.set(this.currentBranch, node.parentId);
    }
  }

  private applyInverse(node: CommitNode<S>): void {
    if (this.version !== node.resultVersion) {
      throw new VersionConflictError(
        `cannot undo ${node.id}: state has changed (version ${this.version} != ${node.resultVersion})`,
        node.resultVersion,
        this.version,
      );
    }
    const draft = structuredClone(this.committed);
    for (const cmd of [...node.commands].reverse()) {
      cmd.undo(draft);
    }
    this.committed = draft;
    this.version = node.baseVersion;
  }

  /** 从当前顶端创建分支。 */
  branch(name: string): void {
    if (this.branches.has(name)) throw new Error(`branch "${name}" already exists`);
    this.branches.set(name, this.branches.get(this.currentBranch) ?? null);
  }

  /** 切换分支：通过回放重建该分支状态，不影响其他分支历史。 */
  checkout(name: string): void {
    if (!this.branches.has(name)) throw new Error(`unknown branch "${name}"`);
    this.currentBranch = name;
    this.committed = this.replay(name);
    const tipId = this.branches.get(name);
    this.version = tipId ? this.commits.get(tipId)!.resultVersion : 0;
  }

  /** 从初始状态回放指定分支（默认当前分支）的全部命令，重建状态。 */
  replay(branchName: string = this.currentBranch): S {
    const chain: CommitNode<S>[] = [];
    let cursor = this.branches.get(branchName) ?? null;
    while (cursor !== null) {
      const node = this.commits.get(cursor)!;
      chain.push(node);
      cursor = node.parentId;
    }
    const state = structuredClone(this.initial);
    for (const node of chain.reverse()) {
      for (const cmd of node.commands) cmd.execute(state);
    }
    return state;
  }

  /** 指定分支（默认当前分支）的历史，自顶向根。 */
  history(branchName: string = this.currentBranch): CommitNode<S>[] {
    const chain: CommitNode<S>[] = [];
    let cursor = this.branches.get(branchName) ?? null;
    while (cursor !== null) {
      const node = this.commits.get(cursor)!;
      chain.push(node);
      cursor = node.parentId;
    }
    return chain;
  }
}
