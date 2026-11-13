import { GovernanceError, governanceError } from "./errors.ts";
import type { Amount, Position } from "./types.ts";
import { normalizeBlockNumber, toBigInt } from "./types.ts";
import { quorumRequired } from "./governance.ts";

export type BalanceModel = "current" | "snapshot";
export type BallotOutcome = "passed" | "defeated";
export type FlashLoanOperationType = "borrow" | "vote" | "repay";

export interface FlashLoanAccount {
  id: string;
  balance: Amount;
}

export interface FlashLoanModel {
  accounts: readonly FlashLoanAccount[] | Readonly<Record<string, Amount>>;
  attacker: string;
  proposalId?: string;
  blockNumber?: number;
  totalSupply?: Amount;
  snapshotBalances?: Readonly<Record<string, Amount>>;
  quorumNumerator?: Amount;
  quorumDenominator?: Amount;
  lender?: string;
  againstVoters?: readonly string[];
  minTransactions?: 1 | 2;
  maxTransactions?: 1 | 2;
}

export interface FlashLoanOperation {
  type: FlashLoanOperationType;
  account: string;
  counterparty?: string;
  amount?: bigint;
  support?: "for";
  weight?: bigint;
  balanceBefore?: bigint;
  balanceAfter?: bigint;
  balanceModel?: BalanceModel;
}

export interface WitnessTransaction {
  blockNumber: number;
  txIndex: number;
  operations: readonly FlashLoanOperation[];
}

export interface FlashLoanWitness {
  mode: BalanceModel;
  proposalId: string;
  blockNumber: number;
  shortest: true;
  transactionCount: number;
  transactions: readonly WitnessTransaction[];
  attacker: string;
  lender: string;
  borrowed: bigint;
  voteWeightObserved: bigint;
  baselineVoteWeight: bigint;
  againstVotes: bigint;
  quorumRequired: bigint;
  baselineOutcome: BallotOutcome;
  attackedOutcome: BallotOutcome;
  changedResult: true;
  balanceBefore: bigint;
  balanceAfter: bigint;
  netBalanceRestored: true;
}

export interface FlashLoanReview {
  currentBalanceWitness: FlashLoanWitness | null;
  snapshotWitness: FlashLoanWitness | null;
  snapshotModelSafe: boolean;
}

export const DEFAULT_FLASH_LOAN_MODEL: FlashLoanModel = {
  accounts: {
    attacker: 20,
    lender: 35,
    voterA: 25,
    voterB: 20,
  },
  attacker: "attacker",
  proposalId: "flash-loan-proposal",
  blockNumber: 100,
  quorumNumerator: 1,
  quorumDenominator: 2,
  againstVoters: ["voterA", "voterB"],
  maxTransactions: 2,
};

interface NormalizedModel {
  balances: Map<string, bigint>;
  snapshotBalances: Map<string, bigint>;
  accounts: string[];
  attacker: string;
  proposalId: string;
  blockNumber: number;
  totalSupply: bigint;
  quorumNumerator: bigint;
  quorumDenominator: bigint;
  lender: string | null;
  againstVoters: string[] | null;
  minTransactions: 1 | 2;
  maxTransactions: 1 | 2;
}

function assertAccount(account: string, label: string): void {
  if (typeof account !== "string" || account.length === 0) {
    throw new TypeError(`${label} must be a non-empty string`);
  }
}

function normalizeAccounts(
  accounts: FlashLoanModel["accounts"]
): Map<string, bigint> {
  const result = new Map<string, bigint>();
  if (Array.isArray(accounts)) {
    for (const account of accounts as readonly FlashLoanAccount[]) {
      assertAccount(account.id, "account id");
      if (result.has(account.id)) {
        governanceError("INVALID_MODEL", `duplicate account ${account.id}`);
      }
      result.set(account.id, toBigInt(account.balance, `balance for ${account.id}`));
    }
  } else {
    for (const [account, balance] of Object.entries(
      accounts as Readonly<Record<string, Amount>>
    )) {
      assertAccount(account, "account id");
      result.set(account, toBigInt(balance, `balance for ${account}`));
    }
  }
  return result;
}

function normalizeModel(model: FlashLoanModel): NormalizedModel {
  if (model === null || typeof model !== "object") {
    governanceError("INVALID_MODEL", "flash-loan model must be an object");
  }

  const balances = normalizeAccounts(model.accounts);
  if (balances.size > 4) {
    governanceError("INVALID_MODEL", "flash-loan model supports at most 4 accounts");
  }
  if (balances.size === 0) {
    governanceError("INVALID_MODEL", "flash-loan model needs at least one account");
  }

  assertAccount(model.attacker, "attacker");
  if (!balances.has(model.attacker)) {
    governanceError("INVALID_MODEL", "attacker is not present in accounts");
  }

  const snapshotBalances = new Map(balances);
  if (model.snapshotBalances !== undefined) {
    for (const [account, balance] of Object.entries(model.snapshotBalances)) {
      assertAccount(account, "snapshot account");
      if (!balances.has(account)) {
        governanceError(
          "INVALID_MODEL",
          `snapshot account ${account} is not present in accounts`
        );
      }
      snapshotBalances.set(account, toBigInt(balance, `snapshot balance for ${account}`));
    }
  }

  if (model.lender !== undefined) {
    assertAccount(model.lender, "lender");
    if (!balances.has(model.lender) || model.lender === model.attacker) {
      governanceError("INVALID_MODEL", "lender must be a non-attacker account");
    }
  }

  if (model.againstVoters !== undefined) {
    for (const account of model.againstVoters) {
      assertAccount(account, "against voter");
      if (!balances.has(account) || account === model.attacker) {
        governanceError(
          "INVALID_MODEL",
          "against voters must be non-attacker accounts"
        );
      }
    }
  }

  const totalSupply =
    model.totalSupply === undefined
      ? sumBalances(snapshotBalances)
      : toBigInt(model.totalSupply, "totalSupply");
  const maxTransactions = model.maxTransactions ?? 2;
  if (maxTransactions !== 1 && maxTransactions !== 2) {
    governanceError("INVALID_MODEL", "maxTransactions must be 1 or 2");
  }
  const minTransactions = model.minTransactions ?? 1;
  if (minTransactions !== 1 && minTransactions !== 2) {
    governanceError("INVALID_MODEL", "minTransactions must be 1 or 2");
  }
  if (minTransactions > maxTransactions) {
    governanceError(
      "INVALID_MODEL",
      "minTransactions cannot exceed maxTransactions"
    );
  }

  return {
    balances,
    snapshotBalances,
    accounts: [...balances.keys()].sort(),
    attacker: model.attacker,
    proposalId: model.proposalId ?? "flash-loan-proposal",
    blockNumber: normalizeBlockNumber(model.blockNumber ?? 100, "blockNumber"),
    totalSupply,
    quorumNumerator: toBigInt(model.quorumNumerator ?? 1n, "quorumNumerator"),
    quorumDenominator: toBigInt(
      model.quorumDenominator ?? 1n,
      "quorumDenominator"
    ),
    lender: model.lender ?? null,
    againstVoters: model.againstVoters
      ? [...new Set(model.againstVoters)].sort()
      : null,
    minTransactions,
    maxTransactions,
  };
}

function sumBalances(balances: ReadonlyMap<string, bigint>): bigint {
  let total = 0n;
  for (const balance of balances.values()) total += balance;
  return total;
}

function passes(
  forVotes: bigint,
  againstVotes: bigint,
  quorum: bigint
): boolean {
  return forVotes >= quorum && forVotes > againstVotes;
}

function outcome(passed: boolean): BallotOutcome {
  return passed ? "passed" : "defeated";
}

function subsets<T>(values: readonly T[]): T[][] {
  const result: T[][] = [[]];
  for (const value of values) {
    const length = result.length;
    for (let index = 0; index < length; index += 1) {
      result.push([...result[index], value]);
    }
  }
  return result;
}

function buildWitness(
  model: NormalizedModel,
  mode: BalanceModel,
  lender: string,
  against: readonly string[],
  amount: bigint,
  transactionCount: number,
  quorum: bigint
): FlashLoanWitness {
  const attackerBalance = model.balances.get(model.attacker) ?? 0n;
  const observedWeight = attackerBalance + amount;
  const againstVotes = against.reduce(
    (total, account) => total + (model.snapshotBalances.get(account) ?? 0n),
    0n
  );

  const borrowOperation: FlashLoanOperation = {
    type: "borrow",
    account: lender,
    counterparty: model.attacker,
    amount,
    balanceBefore: attackerBalance,
    balanceAfter: observedWeight,
  };
  const voteOperation: FlashLoanOperation = {
    type: "vote",
    account: model.attacker,
    support: "for",
    weight: observedWeight,
    balanceModel: mode,
  };
  const repayOperation: FlashLoanOperation = {
    type: "repay",
    account: model.attacker,
    counterparty: lender,
    amount,
    balanceBefore: observedWeight,
    balanceAfter: attackerBalance,
  };

  const transactions: WitnessTransaction[] =
    transactionCount === 1
      ? [
          {
            blockNumber: model.blockNumber,
            txIndex: 0,
            operations: [borrowOperation, voteOperation, repayOperation],
          },
        ]
      : [
          {
            blockNumber: model.blockNumber,
            txIndex: 0,
            operations: [borrowOperation, voteOperation],
          },
          {
            blockNumber: model.blockNumber,
            txIndex: 1,
            operations: [repayOperation],
          },
        ];

  const baselineOutcome = outcome(
    passes(attackerBalance, againstVotes, quorum)
  );
  const attackedOutcome = outcome(passes(observedWeight, againstVotes, quorum));
  if (baselineOutcome === attackedOutcome) {
    throw new GovernanceError(
      "INVALID_MODEL",
      "internal search produced a non-changing witness"
    );
  }

  return {
    mode,
    proposalId: model.proposalId,
    blockNumber: model.blockNumber,
    shortest: true,
    transactionCount,
    transactions,
    attacker: model.attacker,
    lender,
    borrowed: amount,
    voteWeightObserved: observedWeight,
    baselineVoteWeight: attackerBalance,
    againstVotes,
    quorumRequired: quorum,
    baselineOutcome,
    attackedOutcome,
    changedResult: true,
    balanceBefore: attackerBalance,
    balanceAfter: attackerBalance,
    netBalanceRestored: true,
  };
}

/**
 * Searches transaction lengths in ascending order. The returned witness is
 * therefore the shortest borrow/vote/repay cycle in the supplied small model.
 *
 * In snapshot mode the voter's weight is fixed before the borrow transaction,
 * so the attacked and baseline tallies are identical. That mode never emits a
 * flash-loan witness.
 */
export function findFlashLoanWitness(
  model: FlashLoanModel = DEFAULT_FLASH_LOAN_MODEL,
  requestedMode: BalanceModel = "current"
): FlashLoanWitness | null {
  const normalized = normalizeModel(model);
  if (requestedMode === "snapshot") {
    return null;
  }
  if (requestedMode !== "current") {
    throw new TypeError("balance model must be current or snapshot");
  }

  const attackerBalance =
    normalized.balances.get(normalized.attacker) ?? 0n;
  const quorum = quorumRequired(
    normalized.totalSupply,
    normalized.quorumNumerator,
    normalized.quorumDenominator
  );
  const lenderCandidates =
    normalized.lender === null
      ? normalized.accounts.filter(
          (account) =>
            account !== normalized.attacker &&
            (normalized.balances.get(account) ?? 0n) > 0n
        )
      : [normalized.lender];
  const againstCandidates =
    normalized.againstVoters ??
    normalized.accounts.filter((account) => account !== normalized.attacker);
  const againstSubsets =
    normalized.againstVoters === null
      ? subsets(againstCandidates)
      : [normalized.againstVoters];

  const candidates: FlashLoanWitness[] = [];

  for (
    let transactionCount = normalized.minTransactions;
    transactionCount <= normalized.maxTransactions;
    transactionCount += 1
  ) {
    for (const lender of lenderCandidates) {
      const lenderBalance = normalized.balances.get(lender) ?? 0n;
      for (const against of againstSubsets) {
        if (against.includes(lender)) {
          // Opposing ballots in this model have already been cast before the
          // attack block. Keeping the lender out of that set makes the witness
          // unambiguous about the attacker's temporary voting power.
          continue;
        }

        const againstVotes = against.reduce(
          (total, account) =>
            total + (normalized.snapshotBalances.get(account) ?? 0n),
          0n
        );
        const baselineOutcome = passes(
          attackerBalance,
          againstVotes,
          quorum
        );
        if (baselineOutcome) continue;

        const weightNeeded =
          quorum > againstVotes ? quorum : againstVotes + 1n;
        const amount = weightNeeded - attackerBalance;
        if (amount <= 0n || amount > lenderBalance) continue;

        const attackedOutcome = passes(
          attackerBalance + amount,
          againstVotes,
          quorum
        );
        if (!attackedOutcome) continue;

        candidates.push(
          buildWitness(
            normalized,
            "current",
            lender,
            against,
            amount,
            transactionCount,
            quorum
          )
        );
      }
    }
  }

  candidates.sort((left, right) => {
    if (left.transactionCount !== right.transactionCount) {
      return left.transactionCount - right.transactionCount;
    }
    if (left.borrowed !== right.borrowed) {
      return left.borrowed < right.borrowed ? -1 : 1;
    }
    if (left.lender !== right.lender) {
      return left.lender < right.lender ? -1 : 1;
    }
    return left.againstVotes < right.againstVotes ? -1 : 1;
  });

  return candidates[0] ?? null;
}

export const findShortestFlashLoanWitness = findFlashLoanWitness;
export const searchFlashLoanWitness = findFlashLoanWitness;

/**
 * Pending-review callback for hosts that currently read balances at vote
 * time. It returns a concrete same-block exploit witness, or null when the
 * supplied model cannot be flipped by a borrow/vote/repay cycle.
 */
export function currentBalanceReviewCallback(
  model: FlashLoanModel = DEFAULT_FLASH_LOAN_MODEL
): FlashLoanWitness | null {
  return findFlashLoanWitness(model, "current");
}

export const reviewCurrentBalanceVoting = currentBalanceReviewCallback;
export const auditCurrentBalanceVoting = currentBalanceReviewCallback;
export const currentBalanceReview = currentBalanceReviewCallback;
export const currentBalancePendingReview = currentBalanceReviewCallback;

/**
 * Pending-review callback for the corrected snapshot model.
 */
export function snapshotReviewCallback(
  model: FlashLoanModel = DEFAULT_FLASH_LOAN_MODEL
): FlashLoanWitness | null {
  return findFlashLoanWitness(model, "snapshot");
}

export const reviewSnapshotVoting = snapshotReviewCallback;
export const auditSnapshotVoting = snapshotReviewCallback;
export const snapshotBalanceReview = snapshotReviewCallback;

export function reviewFlashLoanScenario(
  model: FlashLoanModel = DEFAULT_FLASH_LOAN_MODEL
): FlashLoanReview {
  const currentBalanceWitness = findFlashLoanWitness(model, "current");
  const snapshotWitness = findFlashLoanWitness(model, "snapshot");
  return {
    currentBalanceWitness,
    snapshotWitness,
    snapshotModelSafe: snapshotWitness === null,
  };
}

export const auditFlashLoan = reviewFlashLoanScenario;
export const flashLoanReview = reviewFlashLoanScenario;

export function flashLoanWitnessPosition(
  witness: FlashLoanWitness,
  txIndex = 0
): Position {
  return { blockNumber: witness.blockNumber, txIndex };
}
