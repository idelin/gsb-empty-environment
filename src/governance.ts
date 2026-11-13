import { CheckpointStore } from "./checkpoints.ts";
import { computeVotingPower, DelegationLedger } from "./delegation.ts";
import { GovernanceError, governanceError } from "./errors.ts";
import { computeActionDigest } from "./hash.ts";
import type {
  Amount,
  BlockInterval,
  Position,
  PositionInput,
} from "./types.ts";
import {
  blockInterval,
  clonePosition,
  comparePositions,
  isBlockInInterval,
  normalizeBlockNumber,
  normalizePosition,
  positionKey,
  toBigInt,
} from "./types.ts";

export type VoteSupport = "for" | "against" | "abstain";
export type VoteSupportInput = VoteSupport | boolean | 0 | 1 | 2 | string;
export type ProposalStatus =
  | "pending"
  | "active"
  | "succeeded"
  | "defeated"
  | "queued"
  | "executed";

export const Vote = {
  For: "for",
  Against: "against",
  Abstain: "abstain",
} as const;

export const VoteSupportCode = {
  Against: 0,
  For: 1,
  Abstain: 2,
} as const;

export const ProposalState = {
  Pending: "pending",
  Active: "active",
  Succeeded: "succeeded",
  Defeated: "defeated",
  Queued: "queued",
  Executed: "executed",
} as const;

export interface GovernanceConfigInput {
  generation?: number;
  quorumNumerator?: Amount;
  quorumDenominator?: Amount;
  quorum?: Amount | { numerator: Amount; denominator: Amount };
  votingDelayBlocks?: number;
  votingDelay?: number;
  votingPeriodBlocks?: number;
  votingPeriod?: number;
  queueDelayBlocks?: number;
  queueDelay?: number;
  queueWindowBlocks?: number;
  queueWindow?: number;
  executionDelayBlocks?: number;
  executionDelay?: number;
  executionWindowBlocks?: number;
  executionWindow?: number;
  [key: string]: unknown;
}

export interface GovernanceConfig {
  readonly generation: number;
  readonly quorumNumerator: bigint;
  readonly quorumDenominator: bigint;
  readonly votingDelayBlocks: number;
  readonly votingPeriodBlocks: number;
  readonly queueDelayBlocks: number;
  readonly queueWindowBlocks: number;
  readonly executionDelayBlocks: number;
  readonly executionWindowBlocks: number;
}

export interface ProposalSchedule {
  readonly createdAt: Position;
  readonly snapshotAt: Position;
  readonly votingDelay: BlockInterval;
  readonly voting: BlockInterval;
  readonly queue: BlockInterval;
}

export interface VoteRecord {
  readonly account: string;
  readonly support: VoteSupport;
  readonly weight: bigint;
  readonly position: Position;
}

export interface PositionFields {
  position?: PositionInput;
  blockNumber?: number;
  block?: number;
  txIndex?: number;
}

export interface ProposalRequest extends PositionFields {
  id?: string;
  proposer: string;
  createdAt?: PositionInput;
  action?: unknown;
  actionDigest?: string;
}

export interface CastVoteRequest extends PositionFields {
  proposalId: string;
  account: string;
  support: VoteSupportInput;
}

export interface QueueRequest extends PositionFields {
  proposalId: string;
}

export interface ExecuteRequest extends PositionFields {
  proposalId: string;
  actionDigest?: string;
  action?: unknown;
  configGeneration?: number;
}

export interface GovernanceOptions {
  config?: GovernanceConfigInput;
  checkpoints?: CheckpointStore;
  delegations?: DelegationLedger;
}

export interface VoteResult {
  proposalId: string;
  account: string;
  support: VoteSupport;
  weight: bigint;
  forVotes: bigint;
  againstVotes: bigint;
  abstainVotes: bigint;
}

export interface ProposalStateView {
  id: string;
  proposer: string;
  status: ProposalStatus;
  createdAt: Position;
  snapshotPosition: Position;
  actionDigest: string;
  configGeneration: number;
  forVotes: bigint;
  againstVotes: bigint;
  abstainVotes: bigint;
  quorumRequired: bigint;
  votes: readonly VoteRecord[];
  queuedAt?: Position;
  executedAt?: Position;
}

const MAX_SAFE_BLOCK = Number.MAX_SAFE_INTEGER;

function configNumber(
  input: GovernanceConfigInput,
  primary: string,
  alias: string,
  fallback: number
): number {
  const value = input[primary] ?? input[alias] ?? fallback;
  return normalizeBlockNumber(value, primary);
}

function parseQuorum(
  input: GovernanceConfigInput
): { numerator: bigint; denominator: bigint } {
  let numerator: Amount | undefined = input.quorumNumerator;
  let denominator: Amount | undefined = input.quorumDenominator;

  if (typeof input.quorum === "string" && input.quorum.includes("/")) {
    const [left, right, ...rest] = input.quorum.split("/");
    if (rest.length > 0 || left.length === 0 || right.length === 0) {
      governanceError("INVALID_CONFIG", "quorum ratio must be N/D");
    }
    numerator = numerator ?? left;
    denominator = denominator ?? right;
  } else if (
    input.quorum !== null &&
    typeof input.quorum === "object" &&
    !Array.isArray(input.quorum)
  ) {
    const ratio = input.quorum as { numerator: Amount; denominator: Amount };
    numerator = numerator ?? ratio.numerator;
    denominator = denominator ?? ratio.denominator;
  } else if (input.quorum !== undefined && numerator === undefined) {
    numerator = input.quorum as Amount;
  }

  const normalizedNumerator = toBigInt(numerator ?? 1n, "quorumNumerator");
  const normalizedDenominator = toBigInt(
    denominator ?? 1n,
    "quorumDenominator"
  );
  if (normalizedDenominator === 0n) {
    governanceError("INVALID_CONFIG", "quorum denominator cannot be zero");
  }

  return {
    numerator: normalizedNumerator,
    denominator: normalizedDenominator,
  };
}

export function normalizeConfig(
  input: GovernanceConfigInput = {}
): GovernanceConfig {
  if (input === null || typeof input !== "object") {
    governanceError("INVALID_CONFIG", "governance config must be an object");
  }

  const generation =
    input.generation === undefined
      ? 0
      : normalizeBlockNumber(input.generation, "config generation");
  const quorum = parseQuorum(input);

  const votingDelayBlocks = configNumber(
    input,
    "votingDelayBlocks",
    "votingDelay",
    0
  );
  const votingPeriodBlocks = configNumber(
    input,
    "votingPeriodBlocks",
    "votingPeriod",
    1
  );
  if (votingPeriodBlocks < 1) {
    governanceError("INVALID_CONFIG", "voting period must contain at least one block");
  }

  const queueDelayBlocks = configNumber(
    input,
    "queueDelayBlocks",
    "queueDelay",
    0
  );
  const queueWindowBlocks = configNumber(
    input,
    "queueWindowBlocks",
    "queueWindow",
    MAX_SAFE_BLOCK
  );
  const executionDelayBlocks = configNumber(
    input,
    "executionDelayBlocks",
    "executionDelay",
    0
  );
  const executionWindowBlocks = configNumber(
    input,
    "executionWindowBlocks",
    "executionWindow",
    1
  );

  return {
    generation,
    quorumNumerator: quorum.numerator,
    quorumDenominator: quorum.denominator,
    votingDelayBlocks,
    votingPeriodBlocks,
    queueDelayBlocks,
    queueWindowBlocks,
    executionDelayBlocks,
    executionWindowBlocks,
  };
}

export function quorumRequired(
  totalSupply: Amount,
  numerator: Amount,
  denominator: Amount
): bigint {
  const supply = toBigInt(totalSupply, "totalSupply");
  const quorumNumerator = toBigInt(numerator, "quorumNumerator");
  const quorumDenominator = toBigInt(denominator, "quorumDenominator");
  if (quorumDenominator === 0n) {
    throw new RangeError("quorum denominator cannot be zero");
  }

  const numeratorProduct = supply * quorumNumerator;
  return (numeratorProduct + quorumDenominator - 1n) / quorumDenominator;
}

export const ceilQuorum = quorumRequired;
export const requiredQuorum = quorumRequired;

export function normalizeVoteSupport(support: VoteSupportInput): VoteSupport {
  if (support === true) return "for";
  if (support === false) return "against";
  if (support === 0) return "against";
  if (support === 1) return "for";
  if (support === 2) return "abstain";

  if (typeof support === "string") {
    const normalized = support.trim().toLowerCase();
    if (normalized === "for" || normalized === "yes" || normalized === "y") {
      return "for";
    }
    if (
      normalized === "against" ||
      normalized === "no" ||
      normalized === "n"
    ) {
      return "against";
    }
    if (
      normalized === "abstain" ||
      normalized === "abstention" ||
      normalized === "abstained"
    ) {
      return "abstain";
    }
  }

  throw new TypeError("vote support must be for, against, abstain, or 0|1|2");
}

/**
 * Immutable balances and delegation graph captured at proposal creation.
 */
export class FrozenProposalSnapshot {
  readonly position: Position;
  readonly balances: ReadonlyMap<string, bigint>;
  readonly delegates: ReadonlyMap<string, string | null>;
  readonly totalSupply: bigint;

  constructor(
    position: Position,
    balances: ReadonlyMap<string, bigint>,
    delegates: ReadonlyMap<string, string | null>,
    totalSupply: bigint
  ) {
    this.position = clonePosition(position);
    this.balances = new Map(balances);
    this.delegates = new Map(delegates);
    this.totalSupply = totalSupply;
  }

  votingPower(account: string): bigint {
    return computeVotingPower(account, this.balances, this.delegates);
  }

  balanceOf(account: string): bigint {
    return this.balances.get(account) ?? 0n;
  }

  delegateOf(account: string): string | null {
    return this.delegates.get(account) ?? null;
  }
}

export class GovernanceProposal {
  readonly id: string;
  readonly proposer: string;
  readonly createdAt: Position;
  readonly snapshot: FrozenProposalSnapshot;
  readonly schedule: ProposalSchedule;
  readonly action: unknown;
  readonly actionDigest: string;
  readonly configGeneration: number;
  readonly config: GovernanceConfig;
  readonly quorumRequired: bigint;

  readonly votes: Map<string, VoteRecord>;
  forVotes: bigint;
  againstVotes: bigint;
  abstainVotes: bigint;
  queuedAt: Position | null;
  executedAt: Position | null;

  constructor(params: {
    id: string;
    proposer: string;
    createdAt: Position;
    snapshot: FrozenProposalSnapshot;
    schedule: ProposalSchedule;
    action: unknown;
    actionDigest: string;
    config: GovernanceConfig;
    quorumRequired: bigint;
  }) {
    this.id = params.id;
    this.proposer = params.proposer;
    this.createdAt = clonePosition(params.createdAt);
    this.snapshot = params.snapshot;
    this.schedule = params.schedule;
    this.action = params.action;
    this.actionDigest = params.actionDigest;
    this.configGeneration = params.config.generation;
    this.config = params.config;
    this.quorumRequired = params.quorumRequired;
    this.votes = new Map();
    this.forVotes = 0n;
    this.againstVotes = 0n;
    this.abstainVotes = 0n;
    this.queuedAt = null;
    this.executedAt = null;
  }

  statusAt(blockNumber: number): ProposalStatus {
    const block = normalizeBlockNumber(blockNumber, "blockNumber");
    if (this.executedAt !== null) return "executed";
    if (this.queuedAt !== null) return "queued";
    if (block < this.schedule.voting.startBlock) return "pending";
    if (block < this.schedule.voting.endBlock) return "active";
    return this.succeededAtEnd() ? "succeeded" : "defeated";
  }

  succeededAtEnd(): boolean {
    return (
      this.forVotes >= this.quorumRequired && this.forVotes > this.againstVotes
    );
  }

  get actionHash(): string {
    return this.actionDigest;
  }

  executionInterval(): BlockInterval | null {
    if (this.queuedAt === null) return null;
    const startBlock =
      this.queuedAt.blockNumber + this.config.executionDelayBlocks;
    const endBlock = startBlock + this.config.executionWindowBlocks;
    return blockInterval(startBlock, endBlock);
  }

  isVotingOpenAt(blockNumber: number): boolean {
    return isBlockInInterval(blockNumber, this.schedule.voting);
  }

  isQueueOpenAt(blockNumber: number): boolean {
    return isBlockInInterval(blockNumber, this.schedule.queue);
  }

  isExecutionOpenAt(blockNumber: number): boolean {
    const interval = this.executionInterval();
    return interval !== null && isBlockInInterval(blockNumber, interval);
  }

  voteList(): readonly VoteRecord[] {
    return [...this.votes.values()].sort((left, right) =>
      comparePositions(left.position, right.position)
    );
  }
}

function buildSnapshot(
  checkpoints: CheckpointStore,
  delegations: DelegationLedger,
  position: Position
): FrozenProposalSnapshot {
  const accounts = new Set<string>([
    ...checkpoints.accounts(),
    ...delegations.accounts(),
  ]);
  const balances = new Map<string, bigint>();
  for (const account of accounts) {
    balances.set(account, checkpoints.balanceAt(account, position));
  }
  const delegates = delegations.delegatesAt(position);
  for (const account of delegates.keys()) accounts.add(account);
  for (const account of accounts) {
    if (!balances.has(account)) {
      balances.set(account, checkpoints.balanceAt(account, position));
    }
  }

  return new FrozenProposalSnapshot(
    position,
    balances,
    delegates,
    checkpoints.totalSupplyAt(position)
  );
}

export function createProposalSnapshot(
  checkpoints: CheckpointStore,
  delegations: DelegationLedger,
  position: PositionInput
): FrozenProposalSnapshot {
  return buildSnapshot(
    checkpoints,
    delegations,
    normalizePosition(position)
  );
}

function buildSchedule(
  createdAt: Position,
  config: GovernanceConfig
): ProposalSchedule {
  const votingStart = createdAt.blockNumber + config.votingDelayBlocks;
  const votingEnd = votingStart + config.votingPeriodBlocks;
  const queueStart = votingEnd + config.queueDelayBlocks;
  const queueEnd =
    config.queueWindowBlocks === MAX_SAFE_BLOCK
      ? MAX_SAFE_BLOCK
      : queueStart + config.queueWindowBlocks;

  return {
    createdAt: clonePosition(createdAt),
    snapshotAt: clonePosition(createdAt),
    votingDelay: blockInterval(createdAt.blockNumber, votingStart),
    voting: blockInterval(votingStart, votingEnd),
    queue: blockInterval(queueStart, queueEnd),
  };
}

export function deriveProposalSchedule(
  createdAt: PositionInput,
  config: GovernanceConfigInput
): ProposalSchedule {
  return buildSchedule(
    normalizePosition(createdAt),
    normalizeConfig(config)
  );
}

function assertAccount(account: string, label = "account"): void {
  if (typeof account !== "string" || account.length === 0) {
    throw new TypeError(`${label} must be a non-empty string`);
  }
}

function isCastVoteRequest(value: unknown): value is CastVoteRequest {
  return (
    value !== null &&
    typeof value === "object" &&
    "proposalId" in value &&
    "account" in value &&
    "support" in value
  );
}

function resolveRequestPosition(
  request: PositionFields & { createdAt?: PositionInput }
): PositionInput | undefined {
  const explicit = request.position ?? request.createdAt;
  if (explicit !== undefined) return explicit;

  const block = request.blockNumber ?? request.block;
  if (block === undefined) return undefined;
  return {
    blockNumber: normalizeBlockNumber(block, "blockNumber"),
    txIndex: normalizeBlockNumber(request.txIndex ?? 0, "txIndex"),
  };
}

export class Governance {
  readonly checkpoints: CheckpointStore;
  readonly delegations: DelegationLedger;
  private currentConfig: GovernanceConfig;
  private readonly proposals: Map<string, GovernanceProposal>;
  private proposalSequence: number;

  constructor(
    optionsOrConfig: GovernanceOptions | GovernanceConfigInput = {}
  ) {
    const options: GovernanceOptions =
      "config" in optionsOrConfig ||
      "checkpoints" in optionsOrConfig ||
      "delegations" in optionsOrConfig
        ? (optionsOrConfig as GovernanceOptions)
        : { config: optionsOrConfig as GovernanceConfigInput };

    this.checkpoints = options.checkpoints ?? new CheckpointStore();
    this.delegations = options.delegations ?? new DelegationLedger();
    this.currentConfig = normalizeConfig(options.config ?? {});
    this.proposals = new Map();
    this.proposalSequence = 0;
  }

  get config(): GovernanceConfig {
    return { ...this.currentConfig };
  }

  get configGeneration(): number {
    return this.currentConfig.generation;
  }

  updateConfig(input: GovernanceConfigInput): GovernanceConfig {
    const generation =
      input.generation === undefined
        ? this.currentConfig.generation + 1
        : normalizeBlockNumber(input.generation, "config generation");
    if (generation <= this.currentConfig.generation) {
      governanceError(
        "INVALID_CONFIG",
        "new config generation must be greater than the current generation"
      );
    }
    this.currentConfig = normalizeConfig({ ...input, generation });
    return this.config;
  }

  setConfig(input: GovernanceConfigInput): GovernanceConfig {
    return this.updateConfig(input);
  }

  createProposal(request: ProposalRequest): GovernanceProposal {
    assertAccount(request.proposer, "proposer");
    const rawPosition = resolveRequestPosition(request);
    if (rawPosition === undefined) {
      throw new TypeError("proposal position is required");
    }
    const createdAt = normalizePosition(rawPosition);

    const id =
      request.id ??
      `proposal-${this.proposalSequence + 1}-${positionKey(createdAt)}`;
    if (this.proposals.has(id)) {
      governanceError("DUPLICATE_PROPOSAL", `proposal ${id} already exists`);
    }
    if (request.action === undefined && request.actionDigest === undefined) {
      throw new TypeError("proposal action or actionDigest is required");
    }

    const digest =
      request.actionDigest ??
      computeActionDigest(request.action);
    if (request.actionDigest !== undefined && request.action !== undefined) {
      const computed = computeActionDigest(request.action);
      if (computed !== request.actionDigest) {
        governanceError(
          "ACTION_DIGEST_MISMATCH",
          "supplied action digest does not match the proposal action"
        );
      }
    }
    validateDigest(digest);

    const config = { ...this.currentConfig };
    const schedule = buildSchedule(createdAt, config);
    const snapshot = buildSnapshot(this.checkpoints, this.delegations, createdAt);
    const proposal = new GovernanceProposal({
      id,
      proposer: request.proposer,
      createdAt,
      snapshot,
      schedule,
      action: request.action,
      actionDigest: digest,
      config,
      quorumRequired: quorumRequired(
        snapshot.totalSupply,
        config.quorumNumerator,
        config.quorumDenominator
      ),
    });

    this.proposals.set(id, proposal);
    this.proposalSequence += 1;
    return proposal;
  }

  propose(request: ProposalRequest): GovernanceProposal {
    return this.createProposal(request);
  }

  castVote(request: CastVoteRequest): VoteResult;
  castVote(
    proposalId: string,
    account: string,
    support: VoteSupportInput,
    position: PositionInput
  ): VoteResult;
  castVote(
    requestOrId: CastVoteRequest | string,
    account?: string,
    support?: VoteSupportInput,
    position?: PositionInput
  ): VoteResult {
    let request: CastVoteRequest;
    if (isCastVoteRequest(requestOrId)) {
      request = requestOrId;
    } else {
      if (account === undefined || support === undefined || position === undefined) {
        throw new TypeError("account, support, and position are required");
      }
      request = {
        proposalId: requestOrId as string,
        account,
        support,
        position,
      };
    }

    const proposal = this.requireProposal(request.proposalId);
    assertAccount(request.account, "voter");
    const rawPosition = resolveRequestPosition(request);
    if (rawPosition === undefined) {
      throw new TypeError("vote position is required");
    }
    const votePosition = normalizePosition(rawPosition);

    if (
      votePosition.blockNumber < proposal.schedule.voting.startBlock ||
      votePosition.blockNumber >= proposal.schedule.voting.endBlock
    ) {
      governanceError(
        "VOTING_NOT_OPEN",
        `vote block ${votePosition.blockNumber} is outside ` +
          `[${proposal.schedule.voting.startBlock}, ` +
          `${proposal.schedule.voting.endBlock})`
      );
    }
    if (comparePositions(votePosition, proposal.createdAt) <= 0) {
      governanceError(
        "VOTING_NOT_OPEN",
        "a vote must occur after proposal creation"
      );
    }
    if (proposal.votes.has(request.account)) {
      governanceError(
        "VOTE_ALREADY_CAST",
        `${request.account} already voted on ${proposal.id}`
      );
    }

    const normalizedSupport = normalizeVoteSupport(request.support);
    const weight = proposal.snapshot.votingPower(request.account);
    if (weight <= 0n) {
      governanceError(
        "NO_VOTING_POWER",
        `${request.account} had no voting power before proposal creation`
      );
    }

    const record: VoteRecord = {
      account: request.account,
      support: normalizedSupport,
      weight,
      position: votePosition,
    };
    proposal.votes.set(request.account, record);
    if (normalizedSupport === "for") proposal.forVotes += weight;
    else if (normalizedSupport === "against") proposal.againstVotes += weight;
    else proposal.abstainVotes += weight;

    return {
      proposalId: proposal.id,
      account: request.account,
      support: normalizedSupport,
      weight,
      forVotes: proposal.forVotes,
      againstVotes: proposal.againstVotes,
      abstainVotes: proposal.abstainVotes,
    };
  }

  vote(
    proposalId: string,
    account: string,
    support: VoteSupportInput,
    position: PositionInput
  ): VoteResult {
    return this.castVote(proposalId, account, support, position);
  }

  submitVote(request: CastVoteRequest): VoteResult {
    return this.castVote(request);
  }

  queueProposal(
    request: QueueRequest
  ): GovernanceProposal;
  queueProposal(
    proposalId: string,
    position: PositionInput
  ): GovernanceProposal;
  queueProposal(
    requestOrId: QueueRequest | string,
    position?: PositionInput
  ): GovernanceProposal {
    const request =
      typeof requestOrId === "string"
        ? { proposalId: requestOrId, position }
        : requestOrId;
    const proposal = this.requireProposal(request.proposalId);
    const rawPosition = resolveRequestPosition(request);
    if (rawPosition === undefined) {
      throw new TypeError("queue position is required");
    }
    const queuePosition = normalizePosition(rawPosition);

    if (proposal.executedAt !== null) {
      governanceError("ALREADY_EXECUTED", `proposal ${proposal.id} executed`);
    }
    if (proposal.queuedAt !== null) {
      governanceError("ALREADY_QUEUED", `proposal ${proposal.id} already queued`);
    }
    if (
      queuePosition.blockNumber < proposal.schedule.voting.endBlock ||
      !isBlockInInterval(queuePosition.blockNumber, proposal.schedule.queue)
    ) {
      governanceError(
        "QUEUE_NOT_OPEN",
        `queue block ${queuePosition.blockNumber} is outside ` +
          `[${proposal.schedule.queue.startBlock}, ` +
          `${proposal.schedule.queue.endBlock})`
      );
    }
    if (!proposal.succeededAtEnd()) {
      governanceError(
        "PROPOSAL_NOT_SUCCEEDED",
        `proposal ${proposal.id} did not satisfy quorum and for > against`
      );
    }

    proposal.queuedAt = queuePosition;
    return proposal;
  }

  queue(
    request: QueueRequest
  ): GovernanceProposal;
  queue(
    proposalId: string,
    position: PositionInput
  ): GovernanceProposal;
  queue(
    requestOrId: QueueRequest | string,
    position?: PositionInput
  ): GovernanceProposal {
    if (typeof requestOrId === "string") {
      return this.queueProposal(requestOrId, position as PositionInput);
    }
    return this.queueProposal(requestOrId);
  }

  executeProposal(
    request: ExecuteRequest
  ): GovernanceProposal;
  executeProposal(
    proposalId: string,
    position: PositionInput,
    actionDigest?: string,
    configGeneration?: number
  ): GovernanceProposal;
  executeProposal(
    requestOrId: ExecuteRequest | string,
    position?: PositionInput,
    digestArgument?: string,
    generationArgument?: number
  ): GovernanceProposal {
    const request: ExecuteRequest =
      typeof requestOrId === "string"
        ? {
            proposalId: requestOrId,
            position: position as PositionInput,
            actionDigest: digestArgument,
            configGeneration: generationArgument,
          }
        : requestOrId;

    const rawPosition = resolveRequestPosition(request);
    if (rawPosition === undefined) {
      throw new TypeError("execution position is required");
    }

    const proposal = this.requireProposal(request.proposalId);
    const executionPosition = normalizePosition(rawPosition);
    if (proposal.executedAt !== null) {
      governanceError("ALREADY_EXECUTED", `proposal ${proposal.id} executed`);
    }
    if (proposal.queuedAt === null) {
      governanceError("NOT_QUEUED", `proposal ${proposal.id} is not queued`);
    }
    if (comparePositions(executionPosition, proposal.queuedAt) < 0) {
      governanceError(
        "EXECUTION_NOT_OPEN",
        "execution cannot precede the queue transaction"
      );
    }

    const interval = proposal.executionInterval();
    if (
      interval === null ||
      !isBlockInInterval(executionPosition.blockNumber, interval)
    ) {
      governanceError(
        "EXECUTION_NOT_OPEN",
        interval === null
          ? "proposal has no execution interval"
          : `execution block ${executionPosition.blockNumber} is outside ` +
            `[${interval.startBlock}, ${interval.endBlock})`
      );
    }

    if (this.currentConfig.generation !== proposal.configGeneration) {
      governanceError(
        "CONFIG_GENERATION_MISMATCH",
        `proposal was created with config generation ` +
          `${proposal.configGeneration}, current generation is ` +
          `${this.currentConfig.generation}`
      );
    }
    if (
      request.configGeneration !== undefined &&
      request.configGeneration !== proposal.configGeneration
    ) {
      governanceError(
        "CONFIG_GENERATION_MISMATCH",
        `execution requested config generation ${request.configGeneration}, ` +
          `proposal is bound to ${proposal.configGeneration}`
      );
    }

    let expectedDigest = request.actionDigest;
    if (request.action !== undefined) {
      const computed = computeActionDigest(request.action);
      if (expectedDigest !== undefined && expectedDigest !== computed) {
        governanceError(
          "ACTION_DIGEST_MISMATCH",
          "execution digest does not match the supplied action"
        );
      }
      expectedDigest = computed;
    }
    if (
      expectedDigest !== undefined &&
      expectedDigest !== proposal.actionDigest
    ) {
      governanceError(
        "ACTION_DIGEST_MISMATCH",
        "execution action digest is not the digest bound to the proposal"
      );
    }

    proposal.executedAt = executionPosition;
    return proposal;
  }

  execute(
    request: ExecuteRequest
  ): GovernanceProposal;
  execute(
    proposalId: string,
    position: PositionInput,
    actionDigest?: string,
    configGeneration?: number
  ): GovernanceProposal;
  execute(
    requestOrId: ExecuteRequest | string,
    position?: PositionInput,
    actionDigest?: string,
    configGeneration?: number
  ): GovernanceProposal {
    if (typeof requestOrId !== "string") {
      return this.executeProposal(requestOrId);
    }
    return this.executeProposal(
      requestOrId,
      position as PositionInput,
      actionDigest,
      configGeneration
    );
  }

  getProposal(proposalId: string): GovernanceProposal | undefined {
    return this.proposals.get(proposalId);
  }

  requireProposal(proposalId: string): GovernanceProposal {
    const proposal = this.proposals.get(proposalId);
    if (proposal === undefined) {
      governanceError("UNKNOWN_PROPOSAL", `proposal ${proposalId} not found`);
    }
    return proposal;
  }

  listProposals(): readonly GovernanceProposal[] {
    return [...this.proposals.values()];
  }

  getVotingPower(proposalId: string, account: string): bigint {
    return this.requireProposal(proposalId).snapshot.votingPower(account);
  }

  hasVoted(proposalId: string, account: string): boolean {
    return this.requireProposal(proposalId).votes.has(account);
  }

  getProposalState(
    proposalId: string,
    positionOrBlock?: PositionInput | number
  ): ProposalStateView {
    const proposal = this.requireProposal(proposalId);
    const status =
      positionOrBlock === undefined
        ? proposal.statusAt(proposal.createdAt.blockNumber)
        : proposal.statusAt(
            typeof positionOrBlock === "number"
              ? positionOrBlock
              : normalizePosition(positionOrBlock).blockNumber
          );

    return {
      id: proposal.id,
      proposer: proposal.proposer,
      status,
      createdAt: clonePosition(proposal.createdAt),
      snapshotPosition: clonePosition(proposal.snapshot.position),
      actionDigest: proposal.actionDigest,
      configGeneration: proposal.configGeneration,
      forVotes: proposal.forVotes,
      againstVotes: proposal.againstVotes,
      abstainVotes: proposal.abstainVotes,
      quorumRequired: proposal.quorumRequired,
      votes: proposal.voteList(),
      queuedAt: proposal.queuedAt ?? undefined,
      executedAt: proposal.executedAt ?? undefined,
    };
  }

  getProposalStatus(
    proposalId: string,
    blockNumber: number
  ): ProposalStatus {
    return this.requireProposal(proposalId).statusAt(blockNumber);
  }
}

export const GovernanceHost = Governance;
export const GovernanceEngine = Governance;
export const Governor = Governance;

function validateDigest(digest: string): void {
  if (typeof digest !== "string" || digest.length === 0) {
    throw new TypeError("action digest must be a non-empty string");
  }
}
