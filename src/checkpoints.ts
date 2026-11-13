import type { Amount, Position, PositionInput } from "./types.ts";
import {
  clonePosition,
  comparePositions,
  normalizePosition,
  toBigInt,
} from "./types.ts";

export interface BalanceCheckpoint {
  readonly account: string;
  readonly position: Position;
  readonly balance: bigint;
}

export interface SupplyCheckpoint {
  readonly position: Position;
  readonly totalSupply: bigint;
}

interface PositionedValue {
  readonly position: Position;
}

function upperBound<T extends PositionedValue>(
  checkpoints: readonly T[],
  target: Position,
  inclusive: boolean
): number {
  let low = 0;
  let high = checkpoints.length;

  while (low < high) {
    const middle = Math.floor((low + high) / 2);
    const comparison = comparePositions(checkpoints[middle].position, target);
    if (comparison < 0 || (inclusive && comparison === 0)) {
      low = middle + 1;
    } else {
      high = middle;
    }
  }

  return low;
}

function upsert<T extends PositionedValue>(
  checkpoints: T[],
  value: T
): void {
  const comparisonAt = (index: number) =>
    comparePositions(checkpoints[index].position, value.position);

  let low = 0;
  let high = checkpoints.length;
  while (low < high) {
    const middle = Math.floor((low + high) / 2);
    if (comparisonAt(middle) < 0) {
      low = middle + 1;
    } else {
      high = middle;
    }
  }

  if (low < checkpoints.length && comparisonAt(low) === 0) {
    checkpoints[low] = value;
  } else {
    checkpoints.splice(low, 0, value);
  }
}

function latestAtOrBefore<T extends PositionedValue>(
  checkpoints: readonly T[],
  target: Position
): T | undefined {
  const index = upperBound(checkpoints, target, true);
  return index === 0 ? undefined : checkpoints[index - 1];
}

function latestStrictlyBefore<T extends PositionedValue>(
  checkpoints: readonly T[],
  target: Position
): T | undefined {
  const index = upperBound(checkpoints, target, false);
  return index === 0 ? undefined : checkpoints[index - 1];
}

/**
 * Position-addressable balance and supply checkpoints.
 *
 * Query semantics are deliberately strict: a query at P=(block, tx) observes
 * only checkpoints whose position is lexicographically before P. Checkpoints
 * at the same position replace one another.
 */
export class CheckpointStore {
  private readonly balances: Map<string, BalanceCheckpoint[]>;
  private readonly supply: SupplyCheckpoint[];

  constructor() {
    this.balances = new Map();
    this.supply = [];
  }

  recordBalance(
    account: string,
    position: PositionInput,
    balance: Amount
  ): void {
    assertAccount(account);
    const target = normalizePosition(position);
    const normalizedBalance = toBigInt(balance, "balance");
    let checkpoints = this.balances.get(account);
    if (checkpoints === undefined) {
      checkpoints = [];
      this.balances.set(account, checkpoints);
    }

    upsert(checkpoints, {
      account,
      position: clonePosition(target),
      balance: normalizedBalance,
    });
  }

  writeBalance(
    account: string,
    position: PositionInput,
    balance: Amount
  ): void {
    this.recordBalance(account, position, balance);
  }

  setBalance(
    account: string,
    position: PositionInput,
    balance: Amount
  ): void {
    this.recordBalance(account, position, balance);
  }

  recordTotalSupply(position: PositionInput, totalSupply: Amount): void {
    const target = normalizePosition(position);
    upsert(this.supply, {
      position: clonePosition(target),
      totalSupply: toBigInt(totalSupply, "totalSupply"),
    });
  }

  writeTotalSupply(position: PositionInput, totalSupply: Amount): void {
    this.recordTotalSupply(position, totalSupply);
  }

  setTotalSupply(position: PositionInput, totalSupply: Amount): void {
    this.recordTotalSupply(position, totalSupply);
  }

  balanceAt(account: string, position: PositionInput): bigint {
    const target = normalizePosition(position);
    const checkpoints = this.balances.get(account);
    if (checkpoints === undefined) return 0n;
    return latestStrictlyBefore(checkpoints, target)?.balance ?? 0n;
  }

  balanceBefore(account: string, position: PositionInput): bigint {
    return this.balanceAt(account, position);
  }

  getBalanceAt(account: string, position: PositionInput): bigint {
    return this.balanceAt(account, position);
  }

  getBalance(account: string, position: PositionInput): bigint {
    return this.balanceAt(account, position);
  }

  balanceAtOrBefore(account: string, position: PositionInput): bigint {
    const target = normalizePosition(position);
    const checkpoints = this.balances.get(account);
    if (checkpoints === undefined) return 0n;
    return latestAtOrBefore(checkpoints, target)?.balance ?? 0n;
  }

  latestBalance(account: string): bigint {
    const checkpoints = this.balances.get(account);
    if (checkpoints === undefined || checkpoints.length === 0) return 0n;
    return checkpoints[checkpoints.length - 1].balance;
  }

  totalSupplyAt(position: PositionInput): bigint {
    const target = normalizePosition(position);
    return latestStrictlyBefore(this.supply, target)?.totalSupply ?? 0n;
  }

  totalSupplyBefore(position: PositionInput): bigint {
    return this.totalSupplyAt(position);
  }

  getTotalSupplyAt(position: PositionInput): bigint {
    return this.totalSupplyAt(position);
  }

  getTotalSupply(position: PositionInput): bigint {
    return this.totalSupplyAt(position);
  }

  totalSupplyAtOrBefore(position: PositionInput): bigint {
    const target = normalizePosition(position);
    return latestAtOrBefore(this.supply, target)?.totalSupply ?? 0n;
  }

  latestTotalSupply(): bigint {
    if (this.supply.length === 0) return 0n;
    return this.supply[this.supply.length - 1].totalSupply;
  }

  accounts(): string[] {
    return [...this.balances.keys()].sort();
  }

  balanceCheckpoints(account: string): readonly BalanceCheckpoint[] {
    return [...(this.balances.get(account) ?? [])];
  }

  totalSupplyCheckpoints(): readonly SupplyCheckpoint[] {
    return [...this.supply];
  }

  /**
   * Record the complete post-operation state at a position. This is useful
   * for hosts that already index token events and do not use transfer().
   */
  writeSnapshot(
    position: PositionInput,
    balances: Readonly<Record<string, Amount>>,
    totalSupply?: Amount
  ): void {
    for (const [account, balance] of Object.entries(balances)) {
      this.recordBalance(account, position, balance);
    }
    if (totalSupply !== undefined) {
      this.recordTotalSupply(position, totalSupply);
    }
  }

  transfer(
    from: string,
    to: string,
    amount: Amount,
    position: PositionInput
  ): void {
    assertAccount(from);
    assertAccount(to);
    if (from === to) return;

    const value = toBigInt(amount, "transfer amount");
    const fromBalance = this.balanceAtOrBefore(from, position);
    if (fromBalance < value) {
      throw new RangeError(`${from} has insufficient balance for transfer`);
    }

    const toBalance = this.balanceAtOrBefore(to, position);
    this.recordBalance(from, position, fromBalance - value);
    this.recordBalance(to, position, toBalance + value);
  }

  mint(
    account: string,
    amount: Amount,
    position: PositionInput
  ): void {
    assertAccount(account);
    const value = toBigInt(amount, "mint amount");
    this.recordBalance(
      account,
      position,
      this.balanceAtOrBefore(account, position) + value
    );
    this.recordTotalSupply(
      position,
      this.totalSupplyAtOrBefore(position) + value
    );
  }

  burn(
    account: string,
    amount: Amount,
    position: PositionInput
  ): void {
    assertAccount(account);
    const value = toBigInt(amount, "burn amount");
    const accountBalance = this.balanceAtOrBefore(account, position);
    if (accountBalance < value) {
      throw new RangeError(`${account} has insufficient balance for burn`);
    }
    this.recordBalance(account, position, accountBalance - value);
    this.recordTotalSupply(
      position,
      this.totalSupplyAtOrBefore(position) - value
    );
  }
}

export const Checkpoints = CheckpointStore;
export const CheckpointLedger = CheckpointStore;
export const BalanceCheckpointStore = CheckpointStore;

function assertAccount(account: string): void {
  if (typeof account !== "string" || account.length === 0) {
    throw new TypeError("account must be a non-empty string");
  }
}
