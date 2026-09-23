export type CommitId = string;
export type BranchName = string;

export interface RecordedEvent {
  readonly type: string;
}

export interface RecordedEffect {
  readonly type: string;
}

export interface Commit<E extends RecordedEvent, F extends RecordedEffect> {
  readonly id: CommitId;
  readonly parentId: CommitId | null;
  readonly branch: BranchName;
  readonly sequence: number;
  readonly timestamp: string;
  readonly events: readonly E[];
  readonly effects: readonly F[];
}

export interface BranchRecord {
  readonly name: BranchName;
  readonly head: CommitId | null;
}

export interface HistorySnapshot<E extends RecordedEvent, F extends RecordedEffect> {
  readonly mainBranch: BranchName;
  readonly commits: readonly Commit<E, F>[];
  readonly branches: readonly BranchRecord[];
}

export interface Transaction<E extends RecordedEvent, F extends RecordedEffect> {
  readonly events: readonly E[];
  readonly effects?: readonly F[];
}

export interface CommandContext<S> {
  readonly state: S;
  readonly branch: BranchName;
  readonly parentId: CommitId | null;
}

export type CommandHandler<S, C, E extends RecordedEvent, F extends RecordedEffect> = (
  command: C,
  context: CommandContext<S>
) => Transaction<E, F> | Promise<Transaction<E, F>>;

export type Reducer<S, E extends RecordedEvent> = (state: S, event: E) => S;

export type EffectRunner<F extends RecordedEffect, E extends RecordedEvent> = (
  effect: F,
  context: EffectContext<E, F>
) => void | Promise<void>;

export interface EffectContext<E extends RecordedEvent, F extends RecordedEffect> {
  readonly commitId: CommitId;
  readonly branch: BranchName;
  readonly effects: readonly F[];
}

export interface DerivationContext<S, E extends RecordedEvent, F extends RecordedEffect> {
  readonly commitId: CommitId | null;
  readonly state: S;
  events(): readonly E[];
  derive<V>(derivation: Derivation<S, E, F, V>): V;
}

export type Derivation<S, E extends RecordedEvent, F extends RecordedEffect, V> = (
  context: DerivationContext<S, E, F>
) => V;

export interface ReplayStoreOptions<S, C, E extends RecordedEvent, F extends RecordedEffect> {
  readonly initialState: S;
  readonly reduce: Reducer<S, E>;
  readonly handle: CommandHandler<S, C, E, F>;
  readonly runEffect?: EffectRunner<F, E>;
  readonly mainBranch?: BranchName;
  readonly history?: HistorySnapshot<E, F>;
  readonly now?: () => string;
  readonly createId?: () => CommitId;
}
