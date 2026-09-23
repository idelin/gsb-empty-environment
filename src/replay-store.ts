import { randomUUID } from 'node:crypto';
import type {
  BranchName,
  BranchRecord,
  Commit,
  CommitId,
  Derivation,
  DerivationContext,
  EffectRunner,
  HistorySnapshot,
  RecordedEffect,
  RecordedEvent,
  ReplayStoreOptions,
} from './types.ts';

type Head =
  | { readonly kind: 'branch'; readonly name: BranchName }
  | { readonly kind: 'detached'; readonly commitId: CommitId };

function cloneValue<T>(value: T): T {
  return structuredClone(value);
}

function deepFreeze<T>(value: T): T {
  if (value === null || typeof value !== 'object') return value;

  const frozen = value as Record<PropertyKey, unknown>;
  const propertyNames = Reflect.ownKeys(frozen);
  for (const key of propertyNames) {
    const child = frozen[key];
    if (child !== null && typeof child === 'object') deepFreeze(child);
  }
  return Object.freeze(value);
}

export class ReplayStore<S, C, E extends RecordedEvent, F extends RecordedEffect> {
  private readonly initialState: S;
  private readonly reduce: ReplayStoreOptions<S, C, E, F>['reduce'];
  private readonly handle: ReplayStoreOptions<S, C, E, F>['handle'];
  private readonly runEffect: EffectRunner<F, E>;
  private readonly mainBranch: BranchName;
  private readonly now: () => string;
  private readonly createId: () => CommitId;

  private readonly commits = new Map<CommitId, Commit<E, F>>();
  private readonly branches = new Map<BranchName, CommitId | null>();
  private readonly stateCache = new Map<CommitId, S>();
  private head: Head;
  private taskQueue: Promise<unknown> = Promise.resolve();

  constructor(options: ReplayStoreOptions<S, C, E, F>) {
    this.initialState = deepFreeze(cloneValue(options.initialState));
    this.reduce = options.reduce;
    this.handle = options.handle;
    this.runEffect = options.runEffect ?? (() => undefined);
    this.mainBranch = options.mainBranch ?? 'main';
    this.now = options.now ?? (() => new Date().toISOString());
    this.createId = options.createId ?? (() => randomUUID());

    this.branches.set(this.mainBranch, null);
    this.head = { kind: 'branch', name: this.mainBranch };

    if (options.history) {
      this.restore(options.history);
    }
  }

  get main(): BranchName {
    return this.mainBranch;
  }

  status(): {
    readonly detached: boolean;
    readonly head: BranchName | CommitId;
    readonly commitId: CommitId | null;
    readonly main: BranchName;
  } {
    return {
      detached: this.head.kind === 'detached',
      head: this.head.kind === 'branch' ? this.head.name : this.head.commitId,
      commitId: this.currentCommitId(),
      main: this.mainBranch,
    };
  }

  async dispatch(command: C) {
    return this.enqueue(async () => {
      if (this.head.kind !== 'branch') {
        throw new Error(
          'Cannot dispatch from a detached historical commit. Create or checkout a branch first.'
        );
      }

      const branch = this.head.name;
      const parentId = this.branches.get(branch) ?? null;
      const parentState = this.stateAt(parentId);
      const proposed = await this.handle(command, { state: parentState, branch, parentId });

      if (!proposed || !Array.isArray(proposed.events) || proposed.events.length === 0) {
        throw new Error('A replayable transaction must contain at least one state event.');
      }
      if (proposed.effects !== undefined && !Array.isArray(proposed.effects)) {
        throw new Error('Transaction effects must be an array.');
      }

      const events = deepFreeze(cloneValue(proposed.events)) as readonly E[];
      const effects = deepFreeze(
        cloneValue(proposed.effects ?? [])
      ) as readonly F[];

      let nextState = parentState;
      for (const event of events) {
        nextState = deepFreeze(this.reduce(nextState, event));
      }

      const sequence = this.commits.size + 1;
      const commit = deepFreeze({
        id: this.createId(),
        parentId,
        branch,
        sequence,
        timestamp: this.now(),
        events,
        effects,
      } satisfies Commit<E, F>);

      this.commits.set(commit.id, commit);
      this.stateCache.set(commit.id, nextState);
      this.branches.set(branch, commit.id);

      const context = {
        commitId: commit.id,
        branch: commit.branch,
        effects: commit.effects,
      };
      for (const effect of commit.effects) {
        await this.runEffect(effect, context);
      }

      return { commit, state: cloneValue(nextState) };
    });
  }

  checkout(ref: BranchName | CommitId): this {
    if (this.branches.has(ref)) {
      this.head = { kind: 'branch', name: ref };
      return this;
    }

    if (this.commits.has(ref)) {
      this.head = { kind: 'detached', commitId: ref };
      return this;
    }

    throw new Error(`Unknown branch or commit: ${ref}`);
  }

  branch(name: BranchName, fromRef: BranchName | CommitId | null = this.currentCommitId()): this {
    if (this.branches.has(name)) {
      throw new Error(`Branch already exists: ${name}`);
    }

    const head = fromRef === null ? null : this.requireCommit(this.resolveRef(fromRef)).id;
    this.branches.set(name, head);
    this.head = { kind: 'branch', name };
    return this;
  }

  state(ref: BranchName | CommitId | null = this.currentCommitId()): S {
    return cloneValue(this.stateAt(ref === null ? null : this.resolveRef(ref)));
  }

  events(ref: BranchName | CommitId | null = this.currentCommitId()): readonly E[] {
    const commitId = ref === null ? null : this.resolveRef(ref);
    return this.eventsAt(commitId);
  }

  log(ref: BranchName | CommitId | null = this.currentCommitId()): readonly Commit<E, F>[] {
    const commitId = ref === null ? null : this.resolveRef(ref);
    const result: Commit<E, F>[] = [];
    let currentId = commitId;

    while (currentId !== null) {
      const commit = this.requireCommit(currentId);
      result.push(commit);
      currentId = commit.parentId;
    }

    return result;
  }

  derive<V>(
    derivation: Derivation<S, E, F, V>,
    ref: BranchName | CommitId | null = this.currentCommitId()
  ): V {
    const commitId = ref === null ? null : this.resolveRef(ref);
    const state = this.stateAt(commitId);
    const events = this.eventsAt(commitId);
    const cache = new Map<Derivation<S, E, F, unknown>, unknown>();
    const visiting = new Set<Derivation<S, E, F, unknown>>();

    const context: DerivationContext<S, E, F> = {
      commitId,
      state,
      events: () => events,
      derive: (dependency) => evaluate(dependency),
    };

    const evaluate = (current: Derivation<S, E, F, unknown>): unknown => {
      if (cache.has(current)) return cache.get(current);
      if (visiting.has(current)) {
        throw new Error('Cyclic derivation dependency detected.');
      }

      visiting.add(current);
      const value = current(context);
      visiting.delete(current);
      cache.set(current, value);
      return value;
    };

    return evaluate(derivation) as V;
  }

  history(): HistorySnapshot<E, F> {
    const branches: BranchRecord[] = [...this.branches.entries()].map(([name, head]) => ({
      name,
      head,
    }));

    return deepFreeze({
      mainBranch: this.mainBranch,
      commits: [...this.commits.values()],
      branches,
    });
  }

  private restore(snapshot: HistorySnapshot<E, F>): void {
    const copy = deepFreeze(cloneValue(snapshot));
    if (copy.mainBranch !== this.mainBranch) {
      throw new Error('History main branch does not match store configuration.');
    }

    const branchNames = new Set<BranchName>();
    for (const branch of copy.branches) {
      if (branchNames.has(branch.name)) {
        throw new Error(`Duplicate branch in history: ${branch.name}`);
      }
      branchNames.add(branch.name);
      if (branch.head !== null && !copy.commits.some((commit) => commit.id === branch.head)) {
        throw new Error(`Branch ${branch.name} points to a missing commit.`);
      }
    }

    if (!branchNames.has(this.mainBranch)) {
      throw new Error('History is missing the main branch.');
    }

    for (const commit of copy.commits) {
      if (this.commits.has(commit.id)) {
        throw new Error(`Duplicate commit id in history: ${commit.id}`);
      }
      if (commit.parentId !== null && !copy.commits.some((item) => item.id === commit.parentId)) {
        throw new Error(`Commit ${commit.id} points to a missing parent.`);
      }
      this.commits.set(commit.id, commit);
    }

    this.branches.clear();
    for (const branch of copy.branches) {
      this.branches.set(branch.name, branch.head);
    }
    this.head = { kind: 'branch', name: copy.mainBranch };
  }

  private currentCommitId(): CommitId | null {
    if (this.head.kind === 'detached') return this.head.commitId;
    return this.branches.get(this.head.name) ?? null;
  }

  private resolveRef(ref: BranchName | CommitId): CommitId | null {
    if (this.branches.has(ref)) return this.branches.get(ref) ?? null;
    if (this.commits.has(ref)) return ref;
    throw new Error(`Unknown branch or commit: ${ref}`);
  }

  private requireCommit(id: CommitId): Commit<E, F> {
    const commit = this.commits.get(id);
    if (!commit) throw new Error(`Unknown commit: ${id}`);
    return commit;
  }

  private stateAt(commitId: CommitId | null): S {
    if (commitId === null) return cloneValue(this.initialState);

    const cached = this.stateCache.get(commitId);
    if (cached !== undefined) return cached;

    const commit = this.requireCommit(commitId);
    const parentState = this.stateAt(commit.parentId);
    let state = parentState;
    for (const event of commit.events) {
      state = deepFreeze(this.reduce(state, event));
    }

    this.stateCache.set(commitId, state);
    return state;
  }

  private eventsAt(commitId: CommitId | null): readonly E[] {
    const commits: Commit<E, F>[] = [];
    let currentId = commitId;

    while (currentId !== null) {
      const commit = this.requireCommit(currentId);
      commits.push(commit);
      currentId = commit.parentId;
    }

    return commits
      .reverse()
      .flatMap((commit) => [...commit.events]);
  }

  private enqueue<T>(task: () => Promise<T>): Promise<T> {
    const result = this.taskQueue.then(() => task());
    this.taskQueue = result.then(
      () => undefined,
      () => undefined
    );
    return result;
  }
}
