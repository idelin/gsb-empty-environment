import type {
  AccountId,
  AccountOp,
  CdcEvent,
  Lsn,
  ParticipantCommitEvent,
  ShardId,
  TxnId,
} from './types.ts';

export class SnapshotNotReadyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SnapshotNotReadyError';
  }
}

export class ProtocolViolationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ProtocolViolationError';
  }
}

interface ParticipantLeg {
  shard: ShardId;
  lsn: Lsn;
  ops: AccountOp[];
}

interface OpenTxn {
  txnId: TxnId;
  participants: Set<ShardId>;
  arrived: Map<ShardId, ParticipantLeg>;
  minLsn: Lsn;
}

interface ClosedTxn {
  txnId: TxnId;
  maxLsn: Lsn;
  legs: ParticipantLeg[];
}

interface LocalCommit {
  txnId: TxnId;
  lsn: Lsn;
  ops: AccountOp[];
}

export interface SnapshotOptions {
  allowStale?: boolean;
}

export class Snapshot {
  readonly safeTime: Lsn;
  readonly version: number;
  readonly includedTxnIds: readonly TxnId[];
  readonly shardWatermarks: ReadonlyMap<ShardId, Lsn>;
  private readonly balances: ReadonlyMap<AccountId, number>;

  constructor(
    safeTime: Lsn,
    version: number,
    balances: Map<AccountId, number>,
    includedTxnIds: TxnId[],
    shardWatermarks: Map<ShardId, Lsn>,
  ) {
    this.safeTime = safeTime;
    this.version = version;
    this.balances = new Map(balances);
    this.includedTxnIds = Object.freeze([...includedTxnIds]);
    this.shardWatermarks = new Map(shardWatermarks);
  }

  balanceOf(account: AccountId): number {
    const balance = this.balances.get(account);
    if (balance === undefined) {
      throw new Error(`unknown account: ${account}`);
    }
    return balance;
  }

  totalMoney(): number {
    let total = 0;
    for (const balance of this.balances.values()) total += balance;
    return total;
  }

  entries(): IterableIterator<[AccountId, number]> {
    return this.balances.entries();
  }
}

export interface AlignerStatus {
  ready: boolean;
  safeTime: Lsn | null;
  version: number;
  shardWatermarks: Record<ShardId, Lsn | null>;
  openTxnIds: TxnId[];
}

interface Waiter {
  resolve: (snapshot: Snapshot) => void;
}

export class GlobalReadViewAligner {
  private readonly shards: readonly ShardId[];
  private readonly baseBalances: Map<AccountId, number>;
  private readonly accountShard: Map<AccountId, ShardId>;
  private readonly watermarks = new Map<ShardId, Lsn>();
  private readonly lastLsn = new Map<ShardId, Lsn>();
  private readonly openTxns = new Map<TxnId, OpenTxn>();
  private readonly closedTxns: ClosedTxn[] = [];
  private readonly localCommits: LocalCommit[] = [];
  private readonly waiters: Waiter[] = [];
  private safeTime: Lsn | null = null;
  private version = 0;

  constructor(config: {
    shards: ShardId[];
    accounts: Record<AccountId, { shard: ShardId; balance: number }>;
  }) {
    this.shards = Object.freeze([...config.shards]);
    this.baseBalances = new Map();
    this.accountShard = new Map();
    const shardSet = new Set(this.shards);
    for (const [account, info] of Object.entries(config.accounts)) {
      if (!shardSet.has(info.shard)) {
        throw new ProtocolViolationError(
          `account ${account} mapped to unknown shard ${info.shard}`,
        );
      }
      this.baseBalances.set(account, info.balance);
      this.accountShard.set(account, info.shard);
    }
  }

  ingest(event: CdcEvent): void {
    this.checkShard(event.shard);
    const previous = this.lastLsn.get(event.shard);
    if (previous !== undefined && event.lsn <= previous) {
      throw new ProtocolViolationError(
        `out-of-order CDC event on shard ${event.shard}: lsn ${event.lsn} after ${previous}`,
      );
    }
    this.lastLsn.set(event.shard, event.lsn);
    this.watermarks.set(event.shard, event.lsn);

    switch (event.kind) {
      case 'heartbeat':
        break;
      case 'local-commit':
        this.validateOps(event.shard, event.ops);
        this.localCommits.push({ txnId: event.txnId, lsn: event.lsn, ops: event.ops });
        break;
      case 'participant-commit':
        this.validateOps(event.shard, event.ops);
        this.trackParticipant(event);
        break;
    }

    this.advanceSafeTime();
    this.notifyWaiters();
  }

  isReady(): boolean {
    return this.safeTime !== null && this.openTxns.size === 0;
  }

  status(): AlignerStatus {
    const shardWatermarks: Record<ShardId, Lsn | null> = {};
    for (const shard of this.shards) {
      shardWatermarks[shard] = this.watermarks.get(shard) ?? null;
    }
    return {
      ready: this.isReady(),
      safeTime: this.safeTime,
      version: this.version,
      shardWatermarks,
      openTxnIds: [...this.openTxns.keys()],
    };
  }

  snapshot(options: SnapshotOptions = {}): Snapshot {
    if (this.safeTime === null) {
      throw new SnapshotNotReadyError(
        'no safe time yet: at least one shard has not reported any watermark',
    );
    }
    if (this.openTxns.size > 0 && !options.allowStale) {
      throw new SnapshotNotReadyError(
        `causal closure not reached: ${this.openTxns.size} cross-shard transaction(s) still open ` +
          `(safe time pinned at ${this.safeTime}): ${[...this.openTxns.keys()].join(', ')}`,
      );
    }
    return this.buildSnapshot(this.safeTime);
  }

  waitForSnapshot(): Promise<Snapshot> {
    if (this.isReady()) {
      return Promise.resolve(this.snapshot());
    }
    return new Promise((resolve) => {
      this.waiters.push({ resolve });
    });
  }

  private buildSnapshot(at: Lsn): Snapshot {
    const balances = new Map(this.baseBalances);
    const includedTxnIds: TxnId[] = [];
    const apply = (ops: AccountOp[]) => {
      for (const op of ops) {
        balances.set(op.account, (balances.get(op.account) ?? 0) + op.delta);
      }
    };
    for (const commit of this.localCommits) {
      if (commit.lsn <= at) {
        apply(commit.ops);
        includedTxnIds.push(commit.txnId);
      }
    }
    for (const txn of this.closedTxns) {
      if (txn.maxLsn <= at) {
        for (const leg of txn.legs) apply(leg.ops);
        includedTxnIds.push(txn.txnId);
      }
    }
    return new Snapshot(at, this.version, balances, includedTxnIds, this.watermarks);
  }

  private trackParticipant(event: ParticipantCommitEvent): void {
    for (const participant of event.participants) this.checkShard(participant);
    if (this.closedTxns.some((txn) => txn.txnId === event.txnId)) {
      throw new ProtocolViolationError(`duplicate leg for closed transaction ${event.txnId}`);
    }
    let txn = this.openTxns.get(event.txnId);
    if (!txn) {
      txn = {
        txnId: event.txnId,
        participants: new Set(event.participants),
        arrived: new Map(),
        minLsn: event.lsn,
      };
      this.openTxns.set(event.txnId, txn);
    } else if (!sameMembers(txn.participants, event.participants)) {
      throw new ProtocolViolationError(
        `conflicting participant sets for transaction ${event.txnId}`,
      );
    }
    if (txn.arrived.has(event.shard)) {
      throw new ProtocolViolationError(
        `duplicate leg from shard ${event.shard} for transaction ${event.txnId}`,
      );
    }
    txn.arrived.set(event.shard, { shard: event.shard, lsn: event.lsn, ops: event.ops });
    txn.minLsn = Math.min(txn.minLsn, event.lsn);

    const closed = [...txn.participants].every((shard) => txn!.arrived.has(shard));
    if (closed) {
      const legs = [...txn.arrived.values()];
      const maxLsn = Math.max(...legs.map((leg) => leg.lsn));
      this.closedTxns.push({ txnId: txn.txnId, maxLsn, legs });
      this.openTxns.delete(txn.txnId);
    }
  }

  private advanceSafeTime(): void {
    let candidate: Lsn | null = null;
    for (const shard of this.shards) {
      const watermark = this.watermarks.get(shard);
      if (watermark === undefined) {
        candidate = null;
        break;
      }
      candidate = candidate === null ? watermark : Math.min(candidate, watermark);
    }
    if (candidate !== null) {
      for (const txn of this.openTxns.values()) {
        candidate = Math.min(candidate, txn.minLsn - 1);
      }
    }
    if (candidate !== null && (this.safeTime === null || candidate > this.safeTime)) {
      this.safeTime = candidate;
      this.version += 1;
    }
  }

  private notifyWaiters(): void {
    if (!this.isReady() || this.waiters.length === 0) return;
    const snapshot = this.snapshot();
    const pending = this.waiters.splice(0, this.waiters.length);
    for (const waiter of pending) waiter.resolve(snapshot);
  }

  private checkShard(shard: ShardId): void {
    if (!this.shards.includes(shard)) {
      throw new ProtocolViolationError(`unknown shard: ${shard}`);
    }
  }

  private validateOps(shard: ShardId, ops: AccountOp[]): void {
    for (const op of ops) {
      const home = this.accountShard.get(op.account);
      if (home === undefined) {
        throw new ProtocolViolationError(`unknown account: ${op.account}`);
      }
      if (home !== shard) {
        throw new ProtocolViolationError(
          `account ${op.account} lives on shard ${home}, not on ${shard}`,
        );
      }
    }
  }
}

function sameMembers(a: Set<ShardId>, b: ShardId[]): boolean {
  return a.size === b.length && b.every((shard) => a.has(shard));
}
