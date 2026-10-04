import type { Choice, VoteRecord } from './types.ts';

export class VoteConflictError extends Error {
  readonly existing: VoteRecord;
  readonly incoming: VoteRecord;

  constructor(existing: VoteRecord, incoming: VoteRecord) {
    super(
      `conflicting vote content for issue=${incoming.issueId} voter=${incoming.voterId} ` +
        `version=${incoming.version} replica=${incoming.replicaId}`,
    );
    this.name = 'VoteConflictError';
    this.existing = existing;
    this.incoming = incoming;
  }
}

export function compareVoteOrder(a: VoteRecord, b: VoteRecord): number {
  if (a.version !== b.version) return a.version < b.version ? -1 : 1;
  if (a.replicaId !== b.replicaId) return a.replicaId < b.replicaId ? -1 : 1;
  return 0;
}

export function choiceEquals(a: Choice, b: Choice): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === 'candidate' && b.kind === 'candidate') {
    return a.candidateId === b.candidateId;
  }
  return true;
}

export function voteEquals(a: VoteRecord, b: VoteRecord): boolean {
  return (
    a.issueId === b.issueId &&
    a.voterId === b.voterId &&
    a.version === b.version &&
    a.replicaId === b.replicaId &&
    choiceEquals(a.choice, b.choice)
  );
}

function assertNonEmptyString(value: unknown, field: string): asserts value is string {
  if (typeof value !== 'string' || value.length === 0) {
    throw new TypeError(`vote field ${field} must be a non-empty string`);
  }
}

export function validateVote(vote: VoteRecord): void {
  assertNonEmptyString(vote.issueId, 'issueId');
  assertNonEmptyString(vote.voterId, 'voterId');
  assertNonEmptyString(vote.replicaId, 'replicaId');
  if (!Number.isSafeInteger(vote.version) || vote.version < 0) {
    throw new TypeError(`vote field version must be a non-negative safe integer, got ${vote.version}`);
  }
  const choice = vote.choice;
  if (choice === null || typeof choice !== 'object') {
    throw new TypeError('vote field choice must be an object');
  }
  if (choice.kind === 'candidate') {
    assertNonEmptyString(choice.candidateId, 'choice.candidateId');
  } else if (choice.kind !== 'abstain' && choice.kind !== 'retract') {
    throw new TypeError(`unknown choice kind: ${String((choice as Choice).kind)}`);
  }
}

function keyOf(issueId: string, voterId: string): string {
  return `${issueId} ${voterId}`;
}

export class VoteLedger {
  readonly #votes = new Map<string, VoteRecord>();

  get size(): number {
    return this.#votes.size;
  }

  import(records: Iterable<VoteRecord>): void {
    for (const record of records) {
      validateVote(record);
      const key = keyOf(record.issueId, record.voterId);
      const existing = this.#votes.get(key);
      if (existing === undefined) {
        this.#votes.set(key, record);
        continue;
      }
      const order = compareVoteOrder(existing, record);
      if (order === 0) {
        if (!choiceEquals(existing.choice, record.choice)) {
          throw new VoteConflictError(existing, record);
        }
        continue;
      }
      if (order < 0) {
        this.#votes.set(key, record);
      }
    }
  }

  effectiveVotes(issueId?: string): VoteRecord[] {
    const result: VoteRecord[] = [];
    for (const vote of this.#votes.values()) {
      if (issueId === undefined || vote.issueId === issueId) {
        result.push(vote);
      }
    }
    result.sort((a, b) => {
      if (a.issueId !== b.issueId) return a.issueId < b.issueId ? -1 : 1;
      if (a.voterId !== b.voterId) return a.voterId < b.voterId ? -1 : 1;
      return compareVoteOrder(a, b);
    });
    return result;
  }
}
