import { hashPair, normalizeId, toBigInt } from "./canonical.ts";
import {
  authorizationKey,
  normalizeAuthorization,
  normalizeSignature,
} from "./authorization.ts";
import { ledgerError } from "./errors.ts";
import {
  EMPTY_NODES,
  MERKLE_DEPTH,
  assertMerkleProof,
  authorizationTreeEntries,
  buildSparseTree,
  createMerkleProof,
  merklePath,
  merklePathBits,
  proofForAbsentKey,
  verifyMerkleProof,
} from "./merkle.ts";
import {
  authorizationLeafHash,
  stateLeafHash,
  type InternalAuthorizationState,
  type InternalRequestRecord,
} from "./reducer.ts";
import type {
  CompressedSnapshot,
  RequestRecord,
  RequestStatus,
  SnapshotChange,
  SnapshotChangeInput,
  SnapshotLeafState,
  SnapshotVerification,
} from "./types.ts";

interface NormalizedSnapshotState {
  readonly key: string;
  readonly chainId: string;
  readonly authorization: ReturnType<typeof normalizeAuthorization>;
  readonly signature: string;
  readonly totalAmount: bigint;
  readonly consumed: bigint;
  readonly requests: ReadonlyArray<RequestRecord>;
  readonly revoked: boolean;
  readonly invalidated: boolean;
  readonly leaf: string;
}

function normalizeRequestRecords(
  requests: SnapshotLeafState["requests"],
): ReadonlyArray<RequestRecord> {
  const seen = new Set<string>();
  const records: RequestRecord[] = [];
  for (const request of requests ?? []) {
    const requestId = typeof request === "string" ? request : request.requestId;
    const status: RequestStatus =
      typeof request === "string" ? "active" : request.status;
    if (!requestId) {
      throw ledgerError("INVALID_PROOF", "snapshot request id must not be empty");
    }
    if (seen.has(requestId)) {
      throw ledgerError("INVALID_PROOF", `snapshot repeats business request ${requestId}`);
    }
    seen.add(requestId);
    if (status !== "active" && status !== "invalidated" && status !== "revoked") {
      throw ledgerError("INVALID_PROOF", `snapshot request ${requestId} has invalid status`);
    }
    records.push(Object.freeze({ requestId, status }));
  }
  return Object.freeze(records.sort((left, right) => left.requestId.localeCompare(right.requestId)));
}

function normalizeSnapshotState(input: SnapshotLeafState): NormalizedSnapshotState {
  const authorization = normalizeAuthorization(input.authorization);
  const signature = normalizeSignature(input.signature);
  const key = authorizationKey(authorization, signature);
  const totalAmount =
    input.totalAmount === undefined
      ? authorization.totalAmount
      : toBigInt(input.totalAmount, "snapshot.totalAmount");
  if (totalAmount !== authorization.totalAmount) {
    throw ledgerError(
      "INVALID_AUTHORIZATION",
      "snapshot total allowance must equal the signed authorization total",
    );
  }
  const consumed = toBigInt(input.consumed, "snapshot.consumed");
  if (consumed < 0n || consumed > totalAmount) {
    throw ledgerError("INVALID_AMOUNT", "snapshot consumed value is outside [0, totalAmount]");
  }
  const requests = normalizeRequestRecords(input.requests);
  const revoked = input.revoked === true;
  const invalidated = input.invalidated === true;
  const leaf = authorizationLeafHash({
    key,
    authorization,
    consumed,
    requests,
    revoked,
    invalidated,
  });
  return Object.freeze({
    key,
    chainId: authorization.chainId,
    authorization,
    signature,
    totalAmount,
    consumed,
    requests,
    revoked,
    invalidated,
    leaf,
  });
}

export function stateToSnapshotLeafState(
  state: InternalAuthorizationState,
  overrides: Partial<Omit<SnapshotLeafState, "authorization" | "signature">> = {},
): SnapshotLeafState {
  return {
    authorization: state.authorization,
    signature: state.signature,
    totalAmount: overrides.totalAmount ?? state.totalAmount,
    consumed: overrides.consumed ?? state.consumed,
    requests:
      overrides.requests ??
      Array.from(state.requests, ([requestId, record]) => ({
        requestId,
        status: record.status,
      })),
    revoked: overrides.revoked ?? state.revoked,
    invalidated: overrides.invalidated ?? state.invalidated,
  };
}

function currentEntries(
  states: Iterable<InternalAuthorizationState>,
): { key: string; leaf: string }[] {
  return authorizationTreeEntries(states);
}

function proofForState(
  states: ReadonlyArray<InternalAuthorizationState>,
  key: string,
): ReturnType<typeof createMerkleProof> {
  const entries = currentEntries(states);
  const exists = entries.some((entry) => entry.key === key);
  return exists ? createMerkleProof(entries, key) : proofForAbsentKey(entries, key);
}

function knownNodeMap(
  changes: ReadonlyArray<{
    path: string;
    siblings: ReadonlyArray<{ level: number; hash: string }>;
  }>,
): Map<string, string> {
  const known = new Map<string, string>();
  for (const change of changes) {
    for (const sibling of change.siblings) {
      const prefixLength = MERKLE_DEPTH - sibling.level;
      const currentPrefix = change.path.slice(0, prefixLength);
      const flipped =
        currentPrefix.slice(0, -1) + (currentPrefix.endsWith("0") ? "1" : "0");
      const nodeKey = `${sibling.level}:${flipped}`;
      const existing = known.get(nodeKey);
      if (existing !== undefined && existing !== sibling.hash) {
        throw ledgerError(
          "INVALID_PROOF",
          `two snapshot proofs disagree about the sibling root at height ${sibling.level}`,
        );
      }
      known.set(nodeKey, sibling.hash);
    }
  }
  return known;
}

function updatedRootFromProofs(
  changes: ReadonlyArray<{
    path: string;
    newLeaf: string;
    siblings: ReadonlyArray<{ level: number; hash: string }>;
  }>,
): string {
  const known = knownNodeMap(changes);
  const changed = new Map<string, string>();
  for (const change of changes) {
    if (changed.has(change.path)) {
      throw ledgerError("INVALID_PROOF", `snapshot changes the same path twice: ${change.path}`);
    }
    changed.set(change.path, change.newLeaf);
  }

  const emptyNodes = EMPTY_NODES;

  const memo = new Map<string, string>();
  const build = (prefix: string, length: number): string => {
    const cacheKey = `${length}:${prefix}`;
    const cached = memo.get(cacheKey);
    if (cached !== undefined) return cached;

    let hasChange = false;
    for (const path of changed.keys()) {
      if (path.startsWith(prefix)) {
        hasChange = true;
        break;
      }
    }

    let value: string;
    if (!hasChange) {
      const height = MERKLE_DEPTH - length;
      value = known.get(`${height}:${prefix}`) ?? emptyNodes[height];
    } else if (length === MERKLE_DEPTH) {
      const leaf = changed.get(prefix);
      if (leaf === undefined) {
        throw ledgerError("INVALID_PROOF", "changed subtree lost its leaf");
      }
      value = leaf;
    } else {
      value = hashPair(build(`${prefix}0`, length + 1), build(`${prefix}1`, length + 1));
    }
    memo.set(cacheKey, value);
    return value;
  };

  return build("", 0);
}

export function createCompressedSnapshot(
  chainId: string,
  states: ReadonlyArray<InternalAuthorizationState>,
  updates: ReadonlyArray<SnapshotChangeInput>,
  options: { headHash?: string } = {},
): CompressedSnapshot {
  const normalizedChainId = normalizeId(chainId, "snapshot.chainId");
  const baseEntries = currentEntries(states);
  const baseTree = buildSparseTree(baseEntries);
  const changes: SnapshotChange[] = [];
  const normalizedKeys = new Set<string>();

  for (const update of updates) {
    const normalized = normalizeSnapshotState(update.state);
    if (normalized.chainId !== normalizedChainId) {
      throw ledgerError(
        "CHAIN_MISMATCH",
        `snapshot state chain ${normalized.chainId} does not match ${normalizedChainId}`,
      );
    }
    if (update.key !== undefined && update.key !== normalized.key) {
      throw ledgerError("INVALID_AUTHORIZATION", "snapshot key does not match its signed state");
    }
    if (normalizedKeys.has(normalized.key)) {
      throw ledgerError("INVALID_PROOF", `snapshot changes ${normalized.key} twice`);
    }
    normalizedKeys.add(normalized.key);

    const proof = proofForState(states, normalized.key);
    changes.push(
      Object.freeze({
        key: normalized.key,
        state: update.state,
        oldLeaf: proof.leaf,
        newLeaf: normalized.leaf,
        proof,
      }),
    );
  }

  const root =
    changes.length === 0
      ? baseTree.root
      : updatedRootFromProofs(
          changes.map((change) => ({
            path: merklePathBits(change.key),
            newLeaf: change.newLeaf,
            siblings: change.proof.siblings,
          })),
        );

  return deepFreeze({
    version: 1 as const,
    chainId: normalizedChainId,
    headHash:
      options.headHash === undefined
        ? undefined
        : normalizeId(options.headHash, "snapshot.headHash"),
    baseRoot: baseTree.root,
    root,
    changes,
  });
}

export function verifyCompressedSnapshot(
  snapshot: CompressedSnapshot,
  expectedBaseRoot?: string,
): SnapshotVerification {
  if (!snapshot || typeof snapshot !== "object") {
    throw ledgerError("INVALID_PROOF", "compressed snapshot must be an object");
  }
  if (snapshot.version !== 1) {
    throw ledgerError("INVALID_PROOF", "unsupported compressed snapshot version");
  }
  if (!Array.isArray(snapshot.changes)) {
    throw ledgerError("INVALID_PROOF", "compressed snapshot changes must be an array");
  }
  if (expectedBaseRoot !== undefined && snapshot.baseRoot !== expectedBaseRoot) {
    throw ledgerError("SNAPSHOT_CONFLICT", "compressed snapshot base root is stale");
  }
  if (typeof snapshot.baseRoot !== "string" || typeof snapshot.root !== "string") {
    throw ledgerError("INVALID_PROOF", "compressed snapshot roots must be strings");
  }
  const snapshotChainId = normalizeId(snapshot.chainId, "snapshot.chainId");

  const keys = new Set<string>();
  const verified: SnapshotVerification["changes"][number][] = [];
  const normalizedChanges: Array<{
    path: string;
    newLeaf: string;
    siblings: ReadonlyArray<{ level: number; hash: string }>;
  }> = [];

  for (const change of snapshot.changes) {
    if (!change || typeof change !== "object") {
      throw ledgerError("INVALID_PROOF", "snapshot change must be an object");
    }
    const normalized = normalizeSnapshotState(change.state);
    if (normalized.chainId !== snapshotChainId) {
      throw ledgerError("CHAIN_MISMATCH", "snapshot change belongs to another chain");
    }
    if (change.key !== normalized.key) {
      throw ledgerError(
        "INVALID_AUTHORIZATION",
        "snapshot change key does not match the signed authorization identity",
      );
    }
    if (keys.has(normalized.key)) {
      throw ledgerError("INVALID_PROOF", `snapshot changes ${normalized.key} twice`);
    }
    keys.add(normalized.key);

    assertMerkleProof(change.proof, snapshot.baseRoot, change.oldLeaf);
    if (change.proof.key !== normalized.key) {
      throw ledgerError("INVALID_PROOF", "snapshot proof key does not match the changed leaf");
    }
    if (change.proof.leaf !== change.oldLeaf) {
      throw ledgerError("INVALID_PROOF", "snapshot old leaf does not match its proof");
    }
    if (!verifyMerkleProof(change.proof, snapshot.baseRoot, change.oldLeaf)) {
      throw ledgerError("INVALID_PROOF", "snapshot proof failed verification");
    }
    if (normalized.leaf !== change.newLeaf) {
      throw ledgerError(
        "INVALID_PROOF",
        "snapshot new leaf is not bound to total, consumed and request root",
      );
    }
    if (change.proof.path !== merklePath(normalized.key)) {
      throw ledgerError("INVALID_PROOF", "snapshot proof path is not bound to its key");
    }

    verified.push(
      Object.freeze({
        key: normalized.key,
        oldLeaf: change.oldLeaf,
        newLeaf: normalized.leaf,
      }),
    );
    normalizedChanges.push({
      path: merklePathBits(normalized.key),
      newLeaf: normalized.leaf,
      siblings: change.proof.siblings,
    });
  }

  const computedRoot =
    normalizedChanges.length === 0
      ? snapshot.baseRoot
      : updatedRootFromProofs(normalizedChanges);
  if (computedRoot !== snapshot.root) {
    throw ledgerError(
      "INVALID_PROOF",
      "compressed snapshot root does not commit to the changed paths",
    );
  }

  return Object.freeze({
    root: computedRoot,
    baseRoot: snapshot.baseRoot,
    changes: Object.freeze(verified),
  });
}

function deepFreeze<T>(value: T): T {
  if (value && typeof value === "object" && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const child of Object.values(value as Record<string, unknown>)) {
      deepFreeze(child);
    }
  }
  return value;
}

/** Snapshot states are exact post-state assertion, not a proof of history. */
export function snapshotStateToInternal(
  state: SnapshotLeafState,
  recoveredSigner: string,
): InternalAuthorizationState {
  const normalized = normalizeSnapshotState(state);
  const requests = new Map<string, InternalRequestRecord>();
  for (const request of normalized.requests) {
    requests.set(request.requestId, { status: request.status });
  }
  return {
    key: normalized.key,
    authorization: normalized.authorization,
    signature: normalized.signature,
    recoveredSigner,
    totalAmount: normalized.totalAmount,
    consumed: normalized.consumed,
    requests,
    revoked: normalized.revoked,
    invalidated: normalized.invalidated,
  };
}

export function snapshotLeafFromState(state: InternalAuthorizationState): string {
  return stateLeafHash(state);
}
