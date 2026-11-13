import { CheckpointStore } from "./checkpoints.ts";
import { GovernanceError } from "./errors.ts";
import type { Amount, Position, PositionInput } from "./types.ts";
import { clonePosition, normalizePosition, toBigInt } from "./types.ts";

export interface DelegationCheckpoint {
  readonly account: string;
  readonly position: Position;
  readonly delegate: string | null;
}

function upperBound(
  checkpoints: readonly DelegationCheckpoint[],
  target: Position,
  inclusive: boolean
): number {
  let low = 0;
  let high = checkpoints.length;
  while (low < high) {
    const middle = Math.floor((low + high) / 2);
    const checkpoint = checkpoints[middle].position;
    const before =
      checkpoint.blockNumber < target.blockNumber ||
      (checkpoint.blockNumber === target.blockNumber &&
        checkpoint.txIndex < target.txIndex);
    const equal =
      checkpoint.blockNumber === target.blockNumber &&
      checkpoint.txIndex === target.txIndex;
    if (before || (inclusive && equal)) {
      low = middle + 1;
    } else {
      high = middle;
    }
  }
  return low;
}

function latestCheckpoint(
  checkpoints: readonly DelegationCheckpoint[],
  target: Position,
  inclusive: boolean
): DelegationCheckpoint | undefined {
  const index = upperBound(checkpoints, target, inclusive);
  return index === 0 ? undefined : checkpoints[index - 1];
}

function upsertDelegation(
  checkpoints: DelegationCheckpoint[],
  value: DelegationCheckpoint
): void {
  const compare = (index: number) => {
    const existing = checkpoints[index].position;
    if (existing.blockNumber !== value.position.blockNumber) {
      return existing.blockNumber < value.position.blockNumber ? -1 : 1;
    }
    if (existing.txIndex !== value.position.txIndex) {
      return existing.txIndex < value.position.txIndex ? -1 : 1;
    }
    return 0;
  };

  let low = 0;
  let high = checkpoints.length;
  while (low < high) {
    const middle = Math.floor((low + high) / 2);
    if (compare(middle) < 0) low = middle + 1;
    else high = middle;
  }

  if (low < checkpoints.length && compare(low) === 0) {
    checkpoints[low] = value;
  } else {
    checkpoints.splice(low, 0, value);
  }
}

function assertAccount(account: string, label = "account"): void {
  if (typeof account !== "string" || account.length === 0) {
    throw new TypeError(`${label} must be a non-empty string`);
  }
}

/**
 * Delegation is a functional graph (at most one outgoing edge per account).
 * A valid graph is therefore a directed forest when it is acyclic.
 */
export class DelegationLedger {
  private readonly checkpoints: Map<string, DelegationCheckpoint[]>;
  private readonly knownAccounts: Set<string>;

  constructor() {
    this.checkpoints = new Map();
    this.knownAccounts = new Set();
  }

  setDelegate(
    account: string,
    delegate: string | null | undefined,
    position: PositionInput
  ): void {
    assertAccount(account);
    const normalizedDelegate = delegate ?? null;
    if (normalizedDelegate !== null) {
      assertAccount(normalizedDelegate, "delegate");
      if (normalizedDelegate === account) {
        throw new GovernanceError(
          "DELEGATION_CYCLE",
          `account ${account} cannot delegate to itself`
        );
      }
    }

    const target = normalizePosition(position);
    const prospective = this.effectiveDelegatesAt(target);
    prospective.set(account, normalizedDelegate);

    if (hasDelegationCycle(prospective)) {
      throw new GovernanceError(
        "DELEGATION_CYCLE",
        `delegation update for ${account} would create a cycle`
      );
    }

    this.knownAccounts.add(account);
    if (normalizedDelegate !== null) {
      this.knownAccounts.add(normalizedDelegate);
    }

    let accountCheckpoints = this.checkpoints.get(account);
    if (accountCheckpoints === undefined) {
      accountCheckpoints = [];
      this.checkpoints.set(account, accountCheckpoints);
    }
    upsertDelegation(accountCheckpoints, {
      account,
      position: clonePosition(target),
      delegate: normalizedDelegate,
    });
  }

  delegate(
    account: string,
    position: PositionInput
  ): string | null {
    return this.delegateAt(account, position);
  }

  delegateAt(
    account: string,
    position: PositionInput
  ): string | null {
    const target = normalizePosition(position);
    const accountCheckpoints = this.checkpoints.get(account);
    if (accountCheckpoints === undefined) return null;
    return (
      latestCheckpoint(accountCheckpoints, target, false)?.delegate ?? null
    );
  }

  /**
   * Delegates visible strictly before the supplied position.
   */
  delegatesAt(position: PositionInput): Map<string, string | null> {
    const target = normalizePosition(position);
    const result = new Map<string, string | null>();
    for (const account of this.knownAccounts) {
      const checkpoints = this.checkpoints.get(account);
      const checkpoint =
        checkpoints === undefined
          ? undefined
          : latestCheckpoint(checkpoints, target, false);
      if (checkpoint !== undefined) result.set(account, checkpoint.delegate);
    }
    return result;
  }

  /**
   * Delegation graph including checkpoints written at the exact position.
   */
  effectiveDelegatesAt(position: PositionInput): Map<string, string | null> {
    const target = normalizePosition(position);
    const result = new Map<string, string | null>();
    for (const account of this.knownAccounts) {
      const checkpoints = this.checkpoints.get(account);
      const checkpoint =
        checkpoints === undefined
          ? undefined
          : latestCheckpoint(checkpoints, target, true);
      if (checkpoint !== undefined) result.set(account, checkpoint.delegate);
    }
    return result;
  }

  validateForestAt(
    position: PositionInput,
    inclusive = false
  ): boolean {
    const selector = inclusive
      ? this.effectiveDelegatesAt(position)
      : this.delegatesAt(position);
    return !hasDelegationCycle(selector);
  }

  assertForestAt(position: PositionInput, inclusive = false): void {
    if (!this.validateForestAt(position, inclusive)) {
      throw new GovernanceError(
        "DELEGATION_CYCLE",
        `delegation graph at position is cyclic`
      );
    }
  }

  accounts(): string[] {
    return [...this.knownAccounts].sort();
  }

  checkpointsFor(account: string): readonly DelegationCheckpoint[] {
    return [...(this.checkpoints.get(account) ?? [])];
  }

  /**
   * Returns the voting power of `voter` if weights were frozen at `position`.
   * Each account contributes its snapshot balance exactly once, at the root
   * of its delegation chain.
   */
  votingPowerAt(
    voter: string,
    position: PositionInput,
    balances: CheckpointStore
  ): bigint {
    const delegates = this.delegatesAt(position);
    const snapshotBalances = new Map<string, bigint>();
    const accounts = new Set<string>([
      ...balances.accounts(),
      ...this.knownAccounts,
      voter,
    ]);

    for (const account of accounts) {
      snapshotBalances.set(account, balances.balanceAt(account, position));
    }

    return computeVotingPower(voter, snapshotBalances, delegates);
  }
}

export function hasDelegationCycle(
  delegates: ReadonlyMap<string, string | null>
): boolean {
  const confirmedRoots = new Set<string>();

  for (const account of delegates.keys()) {
    if (confirmedRoots.has(account)) continue;

    const path = new Set<string>();
    let current: string | null = account;

    while (current !== null) {
      if (path.has(current)) return true;
      if (confirmedRoots.has(current)) break;

      path.add(current);
      current = delegates.get(current) ?? null;
    }

    for (const visited of path) confirmedRoots.add(visited);
  }

  return false;
}

export function computeVotingPower(
  voter: string,
  balances: ReadonlyMap<string, bigint>,
  delegates: ReadonlyMap<string, string | null>
): bigint {
  assertAccount(voter, "voter");
  let power = 0n;

  for (const [account, balance] of balances) {
    const path = new Set<string>();
    let current = account;

    while (true) {
      if (path.has(current)) {
        throw new GovernanceError(
          "DELEGATION_CYCLE",
          `delegation chain from ${account} contains a cycle`
        );
      }
      path.add(current);

      const delegate = delegates.get(current) ?? null;
      if (delegate === null) {
        if (current === voter) power += balance;
        break;
      }
      current = delegate;
    }
  }

  return power;
}

export function computeVotingPowerFromRecord(
  voter: string,
  balances: Readonly<Record<string, Amount>>,
  delegates: Readonly<Record<string, string | null>>
): bigint {
  const normalizedBalances = new Map<string, bigint>();
  for (const [account, balance] of Object.entries(balances)) {
    normalizedBalances.set(account, toBigInt(balance, `balance for ${account}`));
  }
  return computeVotingPower(voter, normalizedBalances, new Map(Object.entries(delegates)));
}

export const DelegationCheckpoints = DelegationLedger;
