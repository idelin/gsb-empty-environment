import type {
  AnchorInfo,
  AnchorProof,
  AnchorVerificationContext,
  AnchorVerifier,
  AsyncAnchorVerifier,
  BranchInfo,
  Commit,
  ConflictReason,
  ConflictResult,
  ResolveOptions,
  ResolveOptionsInput,
  StreamState,
  StreamStateInput,
  TimestampValue,
} from './types.ts';

export type {
  AnchorInfo,
  AnchorProof,
  AnchorVerificationContext,
  AsyncAnchorVerifier,
  AnchorVerifier,
  AnchorVerifierInput,
  BranchInfo,
  CidLike,
  Commit,
  ConflictReason,
  ConflictResult,
  ResolveOptions,
  ResolveOptionsInput,
  StreamState,
  StreamStateInput,
  TimestampValue,
} from './types.ts';

const SAFE_INTEGER_MAX = BigInt(Number.MAX_SAFE_INTEGER);
const SAFE_INTEGER_MIN = BigInt(Number.MIN_SAFE_INTEGER);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCommitArray(value: unknown): value is readonly Commit[] {
  return Array.isArray(value);
}

function cidToString(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'bigint' || typeof value === 'number') return String(value);

  if (isRecord(value)) {
    const candidate = value['/'];
    if (typeof candidate === 'string') return candidate;
    if (typeof value.value === 'string') return value.value;

    if (typeof value.toString === 'function') {
      const rendered = value.toString();
      if (rendered && rendered !== '[object Object]') return rendered;
    }
  }

  return String(value ?? '');
}

function normalizeCommit(value: unknown): Commit {
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'bigint') {
    return { cid: String(value) };
  }
  if (isRecord(value)) {
    if (value.cid !== undefined) return value as Commit;
    if (value.commitId !== undefined) return { ...value, cid: value.commitId } as Commit;
    if (value.id !== undefined) return { ...value, cid: value.id } as Commit;
    return { ...value, cid: cidToString(value) } as Commit;
  }
  return { cid: cidToString(value) };
}

function commitsOf(input: StreamStateInput): readonly Commit[] | null {
  if (isCommitArray(input)) return input.map(normalizeCommit);
  if (!isRecord(input)) return null;

  const raw = input.log ?? input.commits;
  if (Array.isArray(raw)) return raw.map(normalizeCommit);
  return null;
}

function summaryOf(input: StreamStateInput): StreamState | null {
  if (!isRecord(input) || isCommitArray(input)) return null;
  return input as StreamState;
}

function branchLength(input: StreamStateInput, commits: readonly Commit[] | null): number {
  if (commits) return commits.length;
  const summary = summaryOf(input);
  if (!summary) return 0;

  const declared = summary.length ?? summary.commitCount;
  if (typeof declared === 'number' && Number.isFinite(declared) && declared >= 0) {
    return Math.trunc(declared);
  }
  return 0;
}

function branchTip(input: StreamStateInput, commits: readonly Commit[] | null): string {
  if (commits && commits.length > 0) {
    return cidToString(commits[commits.length - 1].cid);
  }

  const summary = summaryOf(input);
  if (summary) {
    const declared = summary.tip ?? summary.cid;
    if (declared !== undefined) return cidToString(declared);
  }

  return '';
}

function declaredPrefixLength(options: ResolveOptions): number | null {
  if (typeof options.commonPrefixLength === 'number' && Number.isFinite(options.commonPrefixLength)) {
    return Math.max(0, Math.trunc(options.commonPrefixLength));
  }
  if (typeof options.forkIndex === 'number' && Number.isFinite(options.forkIndex)) {
    return Math.max(0, Math.trunc(options.forkIndex) + 1);
  }
  return null;
}

function commonPrefixLength(
  current: readonly Commit[] | null,
  incoming: readonly Commit[] | null,
  options: ResolveOptions,
): number {
  const declared = declaredPrefixLength(options);
  if (declared !== null) {
    const limit = Math.min(
      current?.length ?? Number.MAX_SAFE_INTEGER,
      incoming?.length ?? Number.MAX_SAFE_INTEGER,
    );
    return Math.min(declared, limit);
  }

  if (!current || !incoming) return 0;

  const limit = Math.min(current.length, incoming.length);
  let index = 0;
  while (index < limit) {
    if (cidToString(current[index].cid) !== cidToString(incoming[index].cid)) break;
    index += 1;
  }
  return index;
}

function timestampToBigInt(value: unknown): bigint | null {
  if (value === null || value === undefined) return null;

  if (typeof value === 'bigint') return value;
  if (typeof value === 'number') {
    return Number.isFinite(value) ? BigInt(Math.trunc(value)) : null;
  }
  if (value instanceof Date) {
    const millis = value.getTime();
    return Number.isFinite(millis) ? BigInt(Math.trunc(millis)) : null;
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (trimmed === '') return null;
    if (/^-?\d+$/.test(trimmed)) {
      try {
        return BigInt(trimmed);
      } catch {
        return null;
      }
    }
    const parsed = Date.parse(trimmed);
    return Number.isFinite(parsed) ? BigInt(Math.trunc(parsed)) : null;
  }

  return null;
}

function timestampOfProof(proof: AnchorProof, commit: Commit): bigint | null {
  const proofCandidates = [
    proof.blockTimestamp,
    proof.timestamp,
    proof.blockTime,
    proof.time,
    proof.ts,
  ];

  for (const candidate of proofCandidates) {
    const timestamp = timestampToBigInt(candidate);
    if (timestamp !== null) return timestamp;
  }

  const commitCandidates = [
    commit.anchorTimestamp,
    commit.anchorBlockTimestamp,
    commit.anchorTime,
    commit.blockTimestamp,
    commit.anchored || commit.type === 'anchor' ? commit.timestamp : undefined,
  ];

  for (const candidate of commitCandidates) {
    const timestamp = timestampToBigInt(candidate);
    if (timestamp !== null) return timestamp;
  }

  return null;
}

function publicTimestamp(value: bigint | null): number | bigint | null {
  if (value === null) return null;
  if (value >= SAFE_INTEGER_MIN && value <= SAFE_INTEGER_MAX) return Number(value);
  return value;
}

function proofsOfCommit(commit: Commit): AnchorProof[] {
  const proofs: AnchorProof[] = [];
  const candidates = [
    commit.anchor,
    commit.anchorProof,
    commit.anchorCommit,
    commit.timestampProof,
    commit.proof,
  ];

  for (const candidate of candidates) {
    if (isRecord(candidate)) {
      proofs.push(candidate as AnchorProof);
    } else if (
      candidate !== undefined &&
      candidate !== null &&
      candidate !== false &&
      candidate !== true
    ) {
      proofs.push({ blockTimestamp: candidate as TimestampValue });
    }
  }

  if (
    proofs.length === 0 &&
    (commit.anchorTimestamp !== undefined ||
      commit.anchorBlockTimestamp !== undefined ||
      commit.anchorTime !== undefined ||
      commit.blockTimestamp !== undefined ||
      ((commit.anchored === true || commit.type === 'anchor') &&
        commit.timestamp !== undefined))
  ) {
    proofs.push({
      blockTimestamp:
        commit.anchorTimestamp ??
        commit.anchorBlockTimestamp ??
        commit.anchorTime ??
        commit.blockTimestamp ??
        commit.timestamp,
    });
  }

  return proofs;
}

function timestampFromSummary(summary: StreamState): bigint | null {
  const candidates = [
    summary.anchorTimestamp,
    summary.anchorBlockTimestamp,
    summary.anchorTime,
    isRecord(summary.anchor) ? summary.anchor.blockTimestamp : undefined,
    isRecord(summary.anchor) ? summary.anchor.timestamp : undefined,
    isRecord(summary.anchor) ? summary.anchor.blockTime : undefined,
    isRecord(summary.anchor) ? summary.anchor.time : undefined,
    isRecord(summary.anchorProof) ? summary.anchorProof.blockTimestamp : undefined,
    isRecord(summary.anchorProof) ? summary.anchorProof.timestamp : undefined,
    isRecord(summary.anchorProof) ? summary.anchorProof.blockTime : undefined,
    isRecord(summary.anchorProof) ? summary.anchorProof.time : undefined,
  ];

  for (const candidate of candidates) {
    const timestamp = timestampToBigInt(candidate);
    if (timestamp !== null) return timestamp;
  }
  return null;
}

function summaryProof(summary: StreamState): AnchorProof | null {
  if (isRecord(summary.anchor)) return summary.anchor as AnchorProof;
  if (isRecord(summary.anchorProof)) return summary.anchorProof as AnchorProof;
  if (summary.anchorTimestamp !== undefined) {
    return { blockTimestamp: summary.anchorTimestamp };
  }
  if (summary.anchorBlockTimestamp !== undefined) {
    return { blockTimestamp: summary.anchorBlockTimestamp };
  }
  if (summary.anchorTime !== undefined) {
    return { blockTimestamp: summary.anchorTime };
  }
  return null;
}

function isThenable(value: unknown): value is PromiseLike<unknown> {
  return isRecord(value) && typeof value.then === 'function';
}

function normalizeSyncOptions(
  options: ResolveOptionsInput | undefined,
): ResolveOptions {
  if (typeof options === 'function') {
    return { verifyAnchor: options as AnchorVerifier };
  }
  return options ?? {};
}

function normalizeAsyncOptions(
  options: ResolveOptionsInput | undefined,
): ResolveOptions {
  if (typeof options === 'function') {
    return { verifyAnchorAsync: options as AsyncAnchorVerifier };
  }
  return options ?? {};
}

function validByVerifier(
  proof: AnchorProof,
  commit: Commit,
  context: AnchorVerificationContext,
  verifier: ResolveOptions['verifyAnchor'],
  trustUnverifiedAnchors: boolean,
): boolean {
  if (proof.verified === false || commit.anchorVerified === false) return false;

  if (verifier) {
    const result = verifier(proof, commit, context);
    if (isThenable(result)) {
      throw new TypeError(
        'verifyAnchor returned a promise; use resolveConflictAsync instead',
      );
    }
    return result === true;
  }

  return proof.verified === true || commit.anchorVerified === true || trustUnverifiedAnchors;
}

async function validByVerifierAsync(
  proof: AnchorProof,
  commit: Commit,
  context: AnchorVerificationContext,
  verifier: AsyncAnchorVerifier | undefined,
  trustUnverifiedAnchors: boolean,
): Promise<boolean> {
  if (proof.verified === false || commit.anchorVerified === false) return false;

  if (verifier) {
    try {
      return (await verifier(proof, commit, context)) === true;
    } catch {
      return false;
    }
  }

  return proof.verified === true || commit.anchorVerified === true || trustUnverifiedAnchors;
}

function anchorInfo(
  commit: Commit,
  index: number,
  proof: AnchorProof,
): AnchorInfo {
  return {
    cid: cidToString(commit.cid),
    index,
    timestamp: publicTimestamp(timestampOfProof(proof, commit)),
    proof,
  };
}

function collectAnchorsSync(
  input: StreamStateInput,
  commits: readonly Commit[] | null,
  side: 'current' | 'incoming',
  options: ResolveOptions,
): AnchorInfo[] {
  const anchors: AnchorInfo[] = [];
  const verifier = options.verifyAnchor ?? options.verify ?? options.anchorVerifier;
  const trust = options.trustUnverifiedAnchors === true;
  let sawCredential = false;

  if (commits) {
    for (let index = 0; index < commits.length; index += 1) {
      const commit = commits[index];
      for (const proof of proofsOfCommit(commit)) {
        sawCredential = true;
        const context: AnchorVerificationContext = {
          side,
          index,
          commit,
          branch: commits,
        };
        let valid = false;
        try {
          valid = validByVerifier(proof, commit, context, verifier, trust);
        } catch (error) {
          if (
            error instanceof TypeError &&
            error.message.includes('resolveConflictAsync')
          ) {
            throw error;
          }
          valid = false;
        }
        if (valid) anchors.push(anchorInfo(commit, index, proof));
      }
    }
  }

  const summary = summaryOf(input);
  if (summary) {
    const proof = summary.anchored === false ? null : summaryProof(summary);
    if (proof && !sawCredential) {
      sawCredential = true;
      const syntheticCommit: Commit = {
        cid: branchTip(input, commits),
        anchor: proof,
        anchored: summary.anchored,
      };
      const index = summaryIndex(summary, commits);
      const context: AnchorVerificationContext = {
        side,
        index,
        commit: syntheticCommit,
        branch: commits,
      };
      let valid = false;
      try {
        valid = validByVerifier(proof, syntheticCommit, context, verifier, trust);
      } catch (error) {
        if (
          error instanceof TypeError &&
          error.message.includes('resolveConflictAsync')
        ) {
          throw error;
        }
        valid = false;
      }
      if (valid) {
        anchors.push({
          cid: cidToString(syntheticCommit.cid),
          index,
          timestamp: publicTimestamp(timestampFromSummary(summary)),
          proof,
        });
      }
    } else if (!sawCredential && summary.anchored === true) {
      const syntheticProof: AnchorProof = {
        blockTimestamp: summary.anchorTimestamp,
        verified: true,
      };
      anchors.push({
        cid: branchTip(input, commits),
        index: summaryIndex(summary, commits),
        timestamp: publicTimestamp(timestampFromSummary(summary)),
        proof: syntheticProof,
      });
    }
  }

  anchors.sort((left, right) => left.index - right.index);
  return anchors;
}

async function collectAnchorsAsync(
  input: StreamStateInput,
  commits: readonly Commit[] | null,
  side: 'current' | 'incoming',
  options: ResolveOptions,
): Promise<AnchorInfo[]> {
  const anchors: AnchorInfo[] = [];
  const verifier =
    options.verifyAnchorAsync ??
    options.verifyAnchor ??
    options.verify ??
    options.anchorVerifier;
  const trust = options.trustUnverifiedAnchors === true;
  let sawCredential = false;

  if (commits) {
    for (let index = 0; index < commits.length; index += 1) {
      const commit = commits[index];
      for (const proof of proofsOfCommit(commit)) {
        sawCredential = true;
        const context: AnchorVerificationContext = {
          side,
          index,
          commit,
          branch: commits,
        };
        const valid = await validByVerifierAsync(proof, commit, context, verifier, trust);
        if (valid) anchors.push(anchorInfo(commit, index, proof));
      }
    }
  }

  const summary = summaryOf(input);
  if (summary) {
    const proof = summary.anchored === false ? null : summaryProof(summary);
    if (proof && !sawCredential) {
      sawCredential = true;
      const syntheticCommit: Commit = {
        cid: branchTip(input, commits),
        anchor: proof,
        anchored: summary.anchored,
      };
      const index = summaryIndex(summary, commits);
      const context: AnchorVerificationContext = {
        side,
        index,
        commit: syntheticCommit,
        branch: commits,
      };
      const valid = await validByVerifierAsync(proof, syntheticCommit, context, verifier, trust);
      if (valid) {
        anchors.push({
          cid: cidToString(syntheticCommit.cid),
          index,
          timestamp: publicTimestamp(timestampFromSummary(summary)),
          proof,
        });
      }
    } else if (!sawCredential && summary.anchored === true) {
      const syntheticProof: AnchorProof = {
        blockTimestamp: summary.anchorTimestamp,
        verified: true,
      };
      anchors.push({
        cid: branchTip(input, commits),
        index: summaryIndex(summary, commits),
        timestamp: publicTimestamp(timestampFromSummary(summary)),
        proof: syntheticProof,
      });
    }
  }

  anchors.sort((left, right) => left.index - right.index);
  return anchors;
}

function summaryIndex(summary: StreamState, commits: readonly Commit[] | null): number {
  if (
    typeof summary.anchorIndex === 'number' &&
    Number.isFinite(summary.anchorIndex)
  ) {
    return Math.max(0, Math.trunc(summary.anchorIndex));
  }

  const length = commits?.length ?? branchLength(summary, null);
  return length > 0 ? length - 1 : 0;
}

function effectiveAnchor(
  anchors: readonly AnchorInfo[],
  commonPrefix: number,
): AnchorInfo | null {
  for (const anchor of anchors) {
    if (anchor.index >= commonPrefix) return anchor;
  }
  return null;
}

function makeBranchInfo(
  side: 'current' | 'incoming',
  input: StreamStateInput,
  commits: readonly Commit[] | null,
  anchors: readonly AnchorInfo[],
  commonPrefix: number,
): BranchInfo {
  const anchor = effectiveAnchor(anchors, commonPrefix);
  return {
    side,
    length: branchLength(input, commits),
    tip: branchTip(input, commits),
    anchored: anchor !== null,
    anchor,
    anchors,
  };
}

function compareTimestamps(
  left: number | bigint | null,
  right: number | bigint | null,
): number {
  if (left === null && right === null) return 0;
  if (left === null) return 1;
  if (right === null) return -1;
  const a = BigInt(left);
  const b = BigInt(right);
  return a < b ? -1 : a > b ? 1 : 0;
}

function compareTips(currentTip: string, incomingTip: string): number {
  if (currentTip === incomingTip) return 0;
  return currentTip < incomingTip ? -1 : 1;
}

interface Decision {
  winner: 'current' | 'incoming';
  reason: ConflictReason;
}

function decide(
  current: BranchInfo,
  incoming: BranchInfo,
  commonPrefix: number,
): Decision {
  const forkIndex = commonPrefix - 1;

  if (current.tip === incoming.tip && current.length === incoming.length) {
    return { winner: 'current', reason: 'same-tip' };
  }

  if (current.anchored && !incoming.anchored) {
    return { winner: 'current', reason: 'current-anchored-over-unanchored' };
  }
  if (!current.anchored && incoming.anchored) {
    return { winner: 'incoming', reason: 'incoming-anchored-over-unanchored' };
  }

  if (current.anchored && incoming.anchored) {
    const currentAnchor = current.anchor;
    const incomingAnchor = incoming.anchor;

    if (!currentAnchor || !incomingAnchor) {
      return lengthOrTipDecision(current, incoming);
    }

    if (currentAnchor.index > forkIndex) {
      const incomingOwnsPostForkAnchor = incomingAnchor.index > forkIndex;
      if (!incomingOwnsPostForkAnchor) {
        return { winner: 'current', reason: 'fork-anchor-guard' };
      }

      const timestampOrder = compareTimestamps(
        incomingAnchor.timestamp,
        currentAnchor.timestamp,
      );
      if (timestampOrder >= 0) {
        return { winner: 'current', reason: 'fork-anchor-guard' };
      }
      return { winner: 'incoming', reason: 'earlier-incoming-anchor' };
    }

    const timestampOrder = compareTimestamps(
      currentAnchor.timestamp,
      incomingAnchor.timestamp,
    );
    if (timestampOrder < 0) {
      return { winner: 'current', reason: 'earlier-current-anchor' };
    }
    if (timestampOrder > 0) {
      return { winner: 'incoming', reason: 'earlier-incoming-anchor' };
    }
  }

  return lengthOrTipDecision(current, incoming);
}

function lengthOrTipDecision(
  current: BranchInfo,
  incoming: BranchInfo,
): Decision {
  if (current.length > incoming.length) {
    return { winner: 'current', reason: 'longer-current' };
  }
  if (incoming.length > current.length) {
    return { winner: 'incoming', reason: 'longer-incoming' };
  }

  const tipOrder = compareTips(current.tip, incoming.tip);
  if (tipOrder < 0) {
    return { winner: 'current', reason: 'smaller-current-tip-cid' };
  }
  if (tipOrder > 0) {
    return { winner: 'incoming', reason: 'smaller-incoming-tip-cid' };
  }

  return { winner: 'current', reason: 'current-stable-tie' };
}

function ancestry(
  currentCommits: readonly Commit[] | null,
  incomingCommits: readonly Commit[] | null,
  commonPrefix: number,
): 'same' | 'current-is-ancestor' | 'incoming-is-ancestor' | null {
  if (!currentCommits || !incomingCommits) return null;

  const sameLength = currentCommits.length === incomingCommits.length;
  if (sameLength && commonPrefix === currentCommits.length) return 'same';

  if (commonPrefix === currentCommits.length && currentCommits.length < incomingCommits.length) {
    return 'current-is-ancestor';
  }
  if (
    commonPrefix === incomingCommits.length &&
    incomingCommits.length < currentCommits.length
  ) {
    return 'incoming-is-ancestor';
  }
  return null;
}

function buildResult<T extends StreamStateInput>(
  currentInput: T,
  incomingInput: T,
  current: BranchInfo,
  incoming: BranchInfo,
  commonPrefix: number,
  decision: Decision,
): ConflictResult<T> {
  const winner = decision.winner;
  const selected = winner === 'current' ? currentInput : incomingInput;
  const losing = winner === 'current' ? incomingInput : currentInput;
  const winningBranch = winner === 'current' ? current : incoming;

  return {
    winner,
    winnerSide: winner,
    selected,
    state: selected,
    winningState: selected,
    losingState: losing,
    tip: winningBranch.tip,
    winningTip: winningBranch.tip,
    reason: decision.reason,
    commonPrefixLength: commonPrefix,
    forkIndex: commonPrefix - 1,
    current,
    incoming,
  };
}

function resolveSync<T extends StreamStateInput>(
  currentInput: T,
  incomingInput: T,
  options: ResolveOptionsInput = {},
): ConflictResult<T> {
  const resolvedOptions = normalizeSyncOptions(options);
  const currentCommits = commitsOf(currentInput);
  const incomingCommits = commitsOf(incomingInput);
  const commonPrefix = commonPrefixLength(currentCommits, incomingCommits, resolvedOptions);

  const relation = ancestry(currentCommits, incomingCommits, commonPrefix);
  if (relation === 'same') {
    const currentAnchors = collectAnchorsSync(currentInput, currentCommits, 'current', resolvedOptions);
    const incomingAnchors = collectAnchorsSync(incomingInput, incomingCommits, 'incoming', resolvedOptions);
    const current = makeBranchInfo('current', currentInput, currentCommits, currentAnchors, commonPrefix);
    const incoming = makeBranchInfo('incoming', incomingInput, incomingCommits, incomingAnchors, commonPrefix);
    return buildResult(currentInput, incomingInput, current, incoming, commonPrefix, {
      winner: 'current',
      reason: 'same-tip',
    });
  }

  if (relation === 'current-is-ancestor') {
    const currentAnchors = collectAnchorsSync(currentInput, currentCommits, 'current', resolvedOptions);
    const incomingAnchors = collectAnchorsSync(incomingInput, incomingCommits, 'incoming', resolvedOptions);
    const current = makeBranchInfo('current', currentInput, currentCommits, currentAnchors, commonPrefix);
    const incoming = makeBranchInfo('incoming', incomingInput, incomingCommits, incomingAnchors, commonPrefix);
    return buildResult(currentInput, incomingInput, current, incoming, commonPrefix, {
      winner: 'incoming',
      reason: 'incoming-descends-from-current',
    });
  }

  if (relation === 'incoming-is-ancestor') {
    const currentAnchors = collectAnchorsSync(currentInput, currentCommits, 'current', resolvedOptions);
    const incomingAnchors = collectAnchorsSync(incomingInput, incomingCommits, 'incoming', resolvedOptions);
    const current = makeBranchInfo('current', currentInput, currentCommits, currentAnchors, commonPrefix);
    const incoming = makeBranchInfo('incoming', incomingInput, incomingCommits, incomingAnchors, commonPrefix);
    return buildResult(currentInput, incomingInput, current, incoming, commonPrefix, {
      winner: 'current',
      reason: 'current-descends-from-incoming',
    });
  }

  const currentAnchors = collectAnchorsSync(currentInput, currentCommits, 'current', resolvedOptions);
  const incomingAnchors = collectAnchorsSync(incomingInput, incomingCommits, 'incoming', resolvedOptions);
  const current = makeBranchInfo('current', currentInput, currentCommits, currentAnchors, commonPrefix);
  const incoming = makeBranchInfo('incoming', incomingInput, incomingCommits, incomingAnchors, commonPrefix);
  return buildResult(
    currentInput,
    incomingInput,
    current,
    incoming,
    commonPrefix,
    decide(current, incoming, commonPrefix),
  );
}

async function resolveAsync<T extends StreamStateInput>(
  currentInput: T,
  incomingInput: T,
  options: ResolveOptionsInput = {},
): Promise<ConflictResult<T>> {
  const resolvedOptions = normalizeAsyncOptions(options);
  const currentCommits = commitsOf(currentInput);
  const incomingCommits = commitsOf(incomingInput);
  const commonPrefix = commonPrefixLength(currentCommits, incomingCommits, resolvedOptions);

  const relation = ancestry(currentCommits, incomingCommits, commonPrefix);
  if (relation !== null) {
    const currentAnchors = await collectAnchorsAsync(currentInput, currentCommits, 'current', resolvedOptions);
    const incomingAnchors = await collectAnchorsAsync(incomingInput, incomingCommits, 'incoming', resolvedOptions);
    const current = makeBranchInfo('current', currentInput, currentCommits, currentAnchors, commonPrefix);
    const incoming = makeBranchInfo('incoming', incomingInput, incomingCommits, incomingAnchors, commonPrefix);

    if (relation === 'same') {
      return buildResult(currentInput, incomingInput, current, incoming, commonPrefix, {
        winner: 'current',
        reason: 'same-tip',
      });
    }
    if (relation === 'current-is-ancestor') {
      return buildResult(currentInput, incomingInput, current, incoming, commonPrefix, {
        winner: 'incoming',
        reason: 'incoming-descends-from-current',
      });
    }
    return buildResult(currentInput, incomingInput, current, incoming, commonPrefix, {
      winner: 'current',
      reason: 'current-descends-from-incoming',
    });
  }

  const currentAnchors = await collectAnchorsAsync(currentInput, currentCommits, 'current', resolvedOptions);
  const incomingAnchors = await collectAnchorsAsync(incomingInput, incomingCommits, 'incoming', resolvedOptions);
  const current = makeBranchInfo('current', currentInput, currentCommits, currentAnchors, commonPrefix);
  const incoming = makeBranchInfo('incoming', incomingInput, incomingCommits, incomingAnchors, commonPrefix);

  return buildResult(
    currentInput,
    incomingInput,
    current,
    incoming,
    commonPrefix,
    decide(current, incoming, commonPrefix),
  );
}

/**
 * Resolve two competing stream histories and return the canonical tip.
 *
 * The current state is returned on a deterministic tie so repeated updates do
 * not oscillate.
 */
export function resolveConflict<T extends StreamStateInput>(
  current: T,
  incoming: T,
  options?: ResolveOptionsInput,
): ConflictResult<T> {
  return resolveSync(current, incoming, options);
}

/**
 * Async variant for verifiers that need to perform I/O (for example, fetching
 * an anchor proof or checking revocation status).
 */
export function resolveConflictAsync<T extends StreamStateInput>(
  current: T,
  incoming: T,
  options?: ResolveOptionsInput,
): Promise<ConflictResult<T>> {
  return resolveAsync(current, incoming, options);
}

/** Return only the winning state, for callers that do not need diagnostics. */
export function chooseStreamState<T extends StreamStateInput>(
  current: T,
  incoming: T,
  options?: ResolveOptionsInput,
): T {
  return resolveSync(current, incoming, options).selected;
}

export const resolveStreamState = chooseStreamState;
export const selectTip = chooseStreamState;
export const pickTip = chooseStreamState;
export const resolveConflictState = chooseStreamState;
export const getWinningState = chooseStreamState;

/** Detailed-result aliases for callers that use different terminology. */
export const conflictResolution = resolveConflict;
export const resolveConflictDetailed = resolveConflict;
export const selectBranch = resolveConflict;

/**
 * Small object-oriented wrapper for nodes that keep resolver options around.
 */
export class ConflictResolver {
  private readonly options: ResolveOptions;

  constructor(options: ResolveOptionsInput = {}) {
    this.options = typeof options === 'function'
      ? { verifyAnchor: options as AnchorVerifier }
      : options;
  }

  resolve<T extends StreamStateInput>(current: T, incoming: T): ConflictResult<T> {
    return resolveSync(current, incoming, this.options);
  }

  resolveAsync<T extends StreamStateInput>(
    current: T,
    incoming: T,
  ): Promise<ConflictResult<T>> {
    return resolveAsync(current, incoming, this.options);
  }

  choose<T extends StreamStateInput>(current: T, incoming: T): T {
    return this.resolve(current, incoming).selected;
  }
}
