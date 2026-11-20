export type NodeId = string;
export type StorageObjectId = string;
export type ShardIndex = string | number;

export interface StorageObject {
  readonly id: StorageObjectId;
}

export interface ShardRef {
  readonly nodeId: NodeId;
  readonly round: string;
  readonly index: ShardIndex;
  readonly objectId?: StorageObjectId;
}

export interface ThresholdSpec {
  readonly required: number;
  readonly round?: string;
  readonly shards: readonly ShardRef[];
}

interface BaseKeyNode {
  readonly id: NodeId;
  readonly objectId?: StorageObjectId;
  readonly revoked?: boolean;
}

export interface MaterialNode extends BaseKeyNode {
  readonly kind: "material";
}

export interface StoredNode extends BaseKeyNode {
  readonly kind: "stored";
  readonly objectId: StorageObjectId;
}

export interface DerivedNode extends BaseKeyNode {
  readonly kind: "derived";
  readonly inputs: readonly NodeId[];
}

export interface SealedNode extends BaseKeyNode {
  readonly kind: "sealed";
  readonly backup: NodeId;
  readonly wrappingKeys: readonly NodeId[];
}

export interface ThresholdNode extends BaseKeyNode {
  readonly kind: "threshold";
  readonly threshold: ThresholdSpec;
}

export interface AliasNode extends BaseKeyNode {
  readonly kind: "alias";
  readonly source: NodeId;
}

export interface ExportNode extends BaseKeyNode {
  readonly kind: "export";
  readonly source?: NodeId;
}

export type KeyNode =
  | MaterialNode
  | StoredNode
  | DerivedNode
  | SealedNode
  | ThresholdNode
  | AliasNode
  | ExportNode;

export interface ExportedCopy {
  readonly id: string;
  readonly keyId: NodeId;
  readonly revoked?: boolean;
}

export interface KeySnapshot {
  readonly targetNodeId: NodeId;
  readonly objects: readonly StorageObject[];
  readonly nodes: readonly KeyNode[];
  readonly mustRemainRecoverable?: readonly NodeId[];
  readonly exportedCopies?: readonly ExportedCopy[];
  readonly revokedCredentials?: readonly string[];
}

export type RecoveryStepKind =
  | "material"
  | "stored"
  | "derived"
  | "sealed"
  | "threshold"
  | "alias"
  | "export";

export interface WitnessShard {
  readonly nodeId: NodeId;
  readonly round: string;
  readonly index: ShardIndex;
  readonly objectId: string;
}

export interface RecoveryStep {
  readonly nodeId: NodeId;
  readonly kind: RecoveryStepKind;
  readonly objectId?: StorageObjectId;
  readonly uses: readonly NodeId[];
  readonly shards?: readonly WitnessShard[];
  readonly copyIds?: readonly string[];
}

export interface RecoveryWitness {
  readonly nodeId: NodeId;
  readonly steps: readonly RecoveryStep[];
  readonly copyIds: readonly string[];
}

export interface RecoveryEvaluation {
  readonly deletedObjectIds: readonly StorageObjectId[];
  readonly recoveredNodeIds: readonly NodeId[];
  readonly recovered: ReadonlySet<NodeId>;
  witnessFor(nodeId: NodeId): RecoveryWitness | null;
}

export interface SnapshotValidationError extends Error {
  readonly issues: readonly string[];
}

export type DestructionPlanStatus =
  | "planned"
  | "already-unrecoverable"
  | "impossible"
  | "invalid-snapshot";

export type ImpossiblePlanCode =
  | "preservation-constraint"
  | "exported-copy"
  | "intrinsic-recovery";

export interface MandatoryFailure {
  readonly keyId: NodeId;
  readonly recoverableBeforeDeletion: boolean;
  readonly witnessBeforeDeletion: RecoveryWitness | null;
}

export interface PlannedDestruction {
  readonly status: "planned";
  readonly targetNodeId: NodeId;
  readonly deletedObjectIds: readonly StorageObjectId[];
  readonly targetRecoverableBefore: true;
  readonly targetRecoverableAfter: false;
  readonly targetWitnessBefore: RecoveryWitness;
  readonly mandatoryWitnesses: Readonly<Record<NodeId, RecoveryWitness>>;
}

export interface AlreadyUnrecoverable {
  readonly status: "already-unrecoverable";
  readonly targetNodeId: NodeId;
  readonly deletedObjectIds: readonly [];
  readonly targetRecoverableBefore: false;
  readonly targetRecoverableAfter: false;
  readonly targetWitnessBefore: null;
  readonly mandatoryWitnesses: Readonly<Record<NodeId, RecoveryWitness>>;
}

export interface ImpossibleDestruction {
  readonly status: "impossible";
  readonly targetNodeId: NodeId;
  readonly deletedObjectIds: null;
  readonly code: ImpossiblePlanCode;
  readonly message: string;
  readonly targetRecoverableBefore: boolean;
  readonly exportedCopyIds: readonly string[];
  readonly blockingMandatoryKeys: readonly MandatoryFailure[];
  readonly allObjectsDeletedTargetWitness: RecoveryWitness | null;
}

export interface InvalidSnapshotPlan {
  readonly status: "invalid-snapshot";
  readonly targetNodeId: NodeId | null;
  readonly deletedObjectIds: null;
  readonly issues: readonly string[];
}

export type DestructionPlan =
  | PlannedDestruction
  | AlreadyUnrecoverable
  | ImpossibleDestruction
  | InvalidSnapshotPlan;

export interface DestructionCertificate {
  readonly deletedObjectIds: readonly StorageObjectId[];
  readonly targetNodeId?: NodeId;
}

export type CertificateStatusCode =
  | "certified"
  | "already-unrecoverable"
  | "insufficient-deletion"
  | "preservation-constraint"
  | "not-minimal"
  | "not-lexicographically-minimal"
  | "target-already-unrecoverable"
  | "exported-copy"
  | "invalid-certificate"
  | "invalid-snapshot";

export interface RestoreWitness {
  readonly restoredObjectId: StorageObjectId;
  readonly remainingDeletedObjectIds: readonly StorageObjectId[];
  readonly targetRecoverable: boolean;
  readonly targetWitness: RecoveryWitness | null;
}

export interface CertificateVerification {
  readonly valid: boolean;
  readonly status:
    | "certified"
    | "already-unrecoverable"
    | "rejected";
  readonly code: CertificateStatusCode;
  readonly message: string;
  readonly deletedObjectIds: readonly StorageObjectId[];
  readonly targetRecoverableBefore: boolean | null;
  readonly targetRecoverableAfter: boolean | null;
  readonly targetWitnessBefore: RecoveryWitness | null;
  readonly targetWitnessAfter: RecoveryWitness | null;
  readonly restoreWitnesses: readonly RestoreWitness[];
  readonly mandatoryFailures: readonly MandatoryFailure[];
  readonly exportedCopyIds: readonly string[];
  readonly issues: readonly string[];
}

interface SnapshotModel {
  readonly snapshot: KeySnapshot;
  readonly objectIds: ReadonlySet<StorageObjectId>;
  readonly sortedObjectIds: readonly StorageObjectId[];
  readonly nodesById: ReadonlyMap<NodeId, KeyNode>;
  readonly sortedNodeIds: readonly NodeId[];
  readonly mandatoryNodeIds: readonly NodeId[];
  readonly exportedCopiesByNode: ReadonlyMap<NodeId, readonly ExportedCopy[]>;
}

type InternalStepKind = RecoveryStepKind;

interface InternalProof {
  readonly kind: InternalStepKind;
  readonly objectId?: StorageObjectId;
  readonly uses: readonly NodeId[];
  readonly shards?: readonly WitnessShard[];
  readonly copyIds?: readonly string[];
}

interface InternalEvaluation {
  readonly model: SnapshotModel;
  readonly deletedObjectIds: readonly StorageObjectId[];
  readonly recovered: ReadonlySet<NodeId>;
  readonly proofs: ReadonlyMap<NodeId, InternalProof>;
}

interface ShardCandidate {
  readonly nodeId: NodeId;
  readonly round: string;
  readonly index: ShardIndex;
  readonly indexKey: string;
  readonly objectKey: string;
  readonly displayObjectId: string;
}

interface ThresholdMatch {
  readonly round: string;
  readonly shards: readonly WitnessShard[];
}

function snapshotError(issues: readonly string[]): SnapshotValidationError {
  const error = new Error(issues.join("; ")) as SnapshotValidationError & {
    issues: readonly string[];
  };
  Object.defineProperty(error, "issues", {
    value: Object.freeze([...issues]),
    enumerable: true,
  });
  return error;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

function isShardIndex(value: unknown): value is ShardIndex {
  return (
    (typeof value === "string" && value.length > 0) ||
    (typeof value === "number" && Number.isFinite(value))
  );
}

export function validateSnapshot(snapshot: KeySnapshot): readonly string[] {
  const issues: string[] = [];

  if (!isRecord(snapshot)) {
    return ["snapshot must be an object"];
  }
  if (!isNonEmptyString(snapshot.targetNodeId)) {
    issues.push("targetNodeId must be a non-empty string");
  }
  if (!Array.isArray(snapshot.objects)) {
    issues.push("objects must be an array");
  } else if (snapshot.objects.length > 16) {
    issues.push("objects must contain at most 16 destructible storage objects");
  }
  if (!Array.isArray(snapshot.nodes)) {
    issues.push("nodes must be an array");
  } else if (snapshot.nodes.length > 64) {
    issues.push("nodes must contain at most 64 derivation nodes");
  }

  const objectIds = new Set<string>();
  if (Array.isArray(snapshot.objects)) {
    for (let index = 0; index < snapshot.objects.length; index += 1) {
      const object = snapshot.objects[index] as unknown;
      if (!isRecord(object) || !isNonEmptyString(object.id)) {
        issues.push(`objects[${index}].id must be a non-empty string`);
        continue;
      }
      if (objectIds.has(object.id)) {
        issues.push(`duplicate storage object id: ${object.id}`);
      }
      objectIds.add(object.id);
    }
  }

  const nodesById = new Map<string, KeyNode>();
  if (Array.isArray(snapshot.nodes)) {
    for (let index = 0; index < snapshot.nodes.length; index += 1) {
      const rawNode = snapshot.nodes[index] as unknown;
      if (!isRecord(rawNode) || !isNonEmptyString(rawNode.id)) {
        issues.push(`nodes[${index}].id must be a non-empty string`);
        continue;
      }
      if (nodesById.has(rawNode.id)) {
        issues.push(`duplicate key node id: ${rawNode.id}`);
        continue;
      }
      if (rawNode.objectId !== undefined && !isNonEmptyString(rawNode.objectId)) {
        issues.push(`node ${rawNode.id} has an invalid objectId`);
      } else if (
        isNonEmptyString(rawNode.objectId) &&
        !objectIds.has(rawNode.objectId)
      ) {
        issues.push(
          `node ${rawNode.id} references unknown storage object ${rawNode.objectId}`,
        );
      }
      if (rawNode.revoked !== undefined && typeof rawNode.revoked !== "boolean") {
        issues.push(`node ${rawNode.id} has an invalid revoked flag`);
      }

      switch (rawNode.kind) {
        case "material":
          nodesById.set(rawNode.id, rawNode as unknown as MaterialNode);
          break;
        case "stored":
          if (!isNonEmptyString(rawNode.objectId)) {
            issues.push(`stored node ${rawNode.id} must have an objectId`);
          }
          nodesById.set(rawNode.id, rawNode as unknown as StoredNode);
          break;
        case "derived":
          if (!Array.isArray(rawNode.inputs)) {
            issues.push(`derived node ${rawNode.id} must have an inputs array`);
          } else if (rawNode.inputs.length === 0) {
            issues.push(
              `derived node ${rawNode.id} must have at least one input`,
            );
          }
          nodesById.set(rawNode.id, rawNode as unknown as DerivedNode);
          break;
        case "sealed":
          if (!isNonEmptyString(rawNode.backup)) {
            issues.push(`sealed node ${rawNode.id} must have a backup node id`);
          }
          if (!Array.isArray(rawNode.wrappingKeys)) {
            issues.push(
              `sealed node ${rawNode.id} must have a wrappingKeys array`,
            );
          } else if (rawNode.wrappingKeys.length === 0) {
            issues.push(
              `sealed node ${rawNode.id} must have at least one wrapping key`,
            );
          }
          nodesById.set(rawNode.id, rawNode as unknown as SealedNode);
          break;
        case "threshold": {
          const threshold = rawNode.threshold as unknown;
          if (!isRecord(threshold)) {
            issues.push(`threshold node ${rawNode.id} must have a threshold spec`);
          } else {
            if (
              !Number.isInteger(threshold.required) ||
              (threshold.required as number) < 1
            ) {
              issues.push(
                `threshold node ${rawNode.id} must require a positive integer`,
              );
            }
            if (
              threshold.round !== undefined &&
              !isNonEmptyString(threshold.round)
            ) {
              issues.push(`threshold node ${rawNode.id} has an invalid round`);
            }
            if (!Array.isArray(threshold.shards)) {
              issues.push(`threshold node ${rawNode.id} must have shards`);
            } else {
              for (
                let shardIndex = 0;
                shardIndex < threshold.shards.length;
                shardIndex += 1
              ) {
                const shard = threshold.shards[shardIndex] as unknown;
                if (!isRecord(shard) || !isNonEmptyString(shard.nodeId)) {
                  issues.push(
                    `threshold node ${rawNode.id} shard ${shardIndex} has no nodeId`,
                  );
                  continue;
                }
                if (!isNonEmptyString(shard.round)) {
                  issues.push(
                    `threshold node ${rawNode.id} shard ${shardIndex} has an invalid round`,
                  );
                }
                if (!isShardIndex(shard.index)) {
                  issues.push(
                    `threshold node ${rawNode.id} shard ${shardIndex} has an invalid index`,
                  );
                }
                if (
                  shard.objectId !== undefined &&
                  !isNonEmptyString(shard.objectId)
                ) {
                  issues.push(
                    `threshold node ${rawNode.id} shard ${shardIndex} has an invalid objectId`,
                  );
                } else if (
                  isNonEmptyString(shard.objectId) &&
                  !objectIds.has(shard.objectId)
                ) {
                  issues.push(
                    `threshold node ${rawNode.id} shard ${shardIndex} references unknown storage object ${shard.objectId}`,
                  );
                }
              }
            }
          }
          nodesById.set(rawNode.id, rawNode as unknown as ThresholdNode);
          break;
        }
        case "alias":
          if (!isNonEmptyString(rawNode.source)) {
            issues.push(`alias node ${rawNode.id} must have a source node id`);
          }
          nodesById.set(rawNode.id, rawNode as unknown as AliasNode);
          break;
        case "export":
          if (rawNode.objectId !== undefined) {
            issues.push(`export node ${rawNode.id} cannot be tied to an object`);
          }
          if (
            rawNode.source !== undefined &&
            !isNonEmptyString(rawNode.source)
          ) {
            issues.push(`export node ${rawNode.id} has an invalid source`);
          }
          nodesById.set(rawNode.id, rawNode as unknown as ExportNode);
          break;
        default:
          issues.push(
            `nodes[${index}] has unsupported kind ${String(rawNode.kind)}`,
          );
      }
    }
  }

  for (const node of nodesById.values()) {
    switch (node.kind) {
      case "derived":
        if (Array.isArray(node.inputs)) {
          for (const input of node.inputs) {
            if (!isNonEmptyString(input) || !nodesById.has(input)) {
              issues.push(`node ${node.id} references unknown input ${String(input)}`);
            }
          }
        }
        break;
      case "sealed":
        if (
          isNonEmptyString(node.backup) &&
          !nodesById.has(node.backup)
        ) {
          issues.push(`node ${node.id} references unknown backup ${node.backup}`);
        }
        if (Array.isArray(node.wrappingKeys)) {
          for (const wrapper of node.wrappingKeys) {
            if (!isNonEmptyString(wrapper) || !nodesById.has(wrapper)) {
              issues.push(
                `node ${node.id} references unknown wrapping key ${String(wrapper)}`,
              );
            }
          }
        }
        break;
      case "threshold":
        if (
          isRecord(node.threshold) &&
          Array.isArray(node.threshold.shards)
        ) {
          for (const shard of node.threshold.shards) {
            if (
              isRecord(shard) &&
              isNonEmptyString(shard.nodeId) &&
              !nodesById.has(shard.nodeId)
            ) {
              issues.push(
                `threshold node ${node.id} references unknown shard node ${shard.nodeId}`,
              );
            }
          }
        }
        break;
      case "alias":
        if (
          isNonEmptyString(node.source) &&
          !nodesById.has(node.source)
        ) {
          issues.push(`node ${node.id} references unknown alias source ${node.source}`);
        }
        break;
      case "export":
        if (
          node.source !== undefined &&
          !nodesById.has(node.source)
        ) {
          issues.push(`node ${node.id} references unknown export source ${node.source}`);
        }
        break;
      default:
        break;
    }
  }

  if (
    isNonEmptyString(snapshot.targetNodeId) &&
    !nodesById.has(snapshot.targetNodeId)
  ) {
    issues.push(`targetNodeId references unknown node ${snapshot.targetNodeId}`);
  }

  if (
    snapshot.mustRemainRecoverable !== undefined &&
    !Array.isArray(snapshot.mustRemainRecoverable)
  ) {
    issues.push("mustRemainRecoverable must be an array of node ids");
  } else if (Array.isArray(snapshot.mustRemainRecoverable)) {
    for (const keyId of snapshot.mustRemainRecoverable) {
      if (!isNonEmptyString(keyId) || !nodesById.has(keyId)) {
        issues.push(
          `mustRemainRecoverable references unknown node ${String(keyId)}`,
        );
      }
    }
  }

  const copyIds = new Set<string>();
  if (
    snapshot.exportedCopies !== undefined &&
    !Array.isArray(snapshot.exportedCopies)
  ) {
    issues.push("exportedCopies must be an array");
  } else if (Array.isArray(snapshot.exportedCopies)) {
    for (let index = 0; index < snapshot.exportedCopies.length; index += 1) {
      const copy = snapshot.exportedCopies[index] as unknown;
      if (!isRecord(copy) || !isNonEmptyString(copy.id)) {
        issues.push(`exportedCopies[${index}].id must be a non-empty string`);
        continue;
      }
      if (copyIds.has(copy.id)) {
        issues.push(`duplicate exported copy id: ${copy.id}`);
      }
      copyIds.add(copy.id);
      if (!isNonEmptyString(copy.keyId) || !nodesById.has(copy.keyId)) {
        issues.push(
          `exported copy ${copy.id} references unknown key ${String(copy.keyId)}`,
        );
      }
      if (copy.revoked !== undefined && typeof copy.revoked !== "boolean") {
        issues.push(`exported copy ${copy.id} has an invalid revoked flag`);
      }
    }
  }

  if (
    snapshot.revokedCredentials !== undefined &&
    !Array.isArray(snapshot.revokedCredentials)
  ) {
    issues.push("revokedCredentials must be an array");
  }

  return Object.freeze(issues);
}

function buildModel(snapshot: KeySnapshot): SnapshotModel {
  const nodesById = new Map<string, KeyNode>();
  for (const node of snapshot.nodes) {
    nodesById.set(node.id, node);
  }
  const exportedCopiesByNode = new Map<string, ExportedCopy[]>();
  for (const copy of snapshot.exportedCopies ?? []) {
    const copies = exportedCopiesByNode.get(copy.keyId) ?? [];
    copies.push(copy);
    exportedCopiesByNode.set(copy.keyId, copies);
  }
  for (const copies of exportedCopiesByNode.values()) {
    copies.sort((left, right) => compareStrings(left.id, right.id));
  }
  const mandatoryNodeIds = [...new Set(snapshot.mustRemainRecoverable ?? [])];
  mandatoryNodeIds.sort(compareStrings);
  const sortedObjectIds = snapshot.objects.map((object) => object.id).sort(compareStrings);
  const sortedNodeIds = [...nodesById.keys()].sort(compareStrings);
  return {
    snapshot,
    objectIds: new Set(sortedObjectIds),
    sortedObjectIds,
    nodesById,
    sortedNodeIds,
    mandatoryNodeIds,
    exportedCopiesByNode,
  };
}

function compareStrings(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

function compareStringArrays(
  left: readonly string[],
  right: readonly string[],
): number {
  const length = Math.min(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    const compared = compareStrings(left[index], right[index]);
    if (compared !== 0) return compared;
  }
  return left.length - right.length;
}

function appendUnique(target: string[], values: readonly string[]): void {
  const seen = new Set(target);
  for (const value of values) {
    if (!seen.has(value)) {
      seen.add(value);
      target.push(value);
    }
  }
}

function shardCandidates(
  model: SnapshotModel,
  spec: ThresholdSpec,
  recovered: ReadonlySet<NodeId>,
): ShardCandidate[] {
  const bySignature = new Map<string, ShardCandidate>();
  for (const ref of spec.shards) {
    if (!recovered.has(ref.nodeId)) continue;
    if (spec.round !== undefined && ref.round !== spec.round) continue;
    const node = model.nodesById.get(ref.nodeId);
    if (node === undefined) continue;
    const physicalObjectId = ref.objectId ?? node.objectId;
    const objectKey =
      physicalObjectId === undefined
        ? `\u0000virtual:${ref.nodeId}`
        : `\u0000object:${physicalObjectId}`;
    const indexKey = String(ref.index);
    const signature = JSON.stringify([ref.round, indexKey, objectKey]);
    const candidate: ShardCandidate = {
      nodeId: ref.nodeId,
      round: ref.round,
      index: ref.index,
      indexKey,
      objectKey,
      displayObjectId: physicalObjectId ?? `@virtual:${ref.nodeId}`,
    };
    const current = bySignature.get(signature);
    if (
      current === undefined ||
      compareStrings(candidate.nodeId, current.nodeId) < 0
    ) {
      bySignature.set(signature, candidate);
    }
  }
  return [...bySignature.values()].sort((left, right) => {
    return (
      compareStrings(left.indexKey, right.indexKey) ||
      compareStrings(left.objectKey, right.objectKey) ||
      compareStrings(left.nodeId, right.nodeId)
    );
  });
}

function matchThreshold(
  candidates: readonly ShardCandidate[],
  required: number,
): ShardCandidate[] | null {
  if (required < 1 || candidates.length < required) return null;

  const indexKeys = [...new Set(candidates.map((candidate) => candidate.indexKey))];
  indexKeys.sort(compareStrings);

  const edgesByIndex = new Map<string, Set<string>>();
  const candidateByEdge = new Map<string, ShardCandidate>();
  for (const candidate of candidates) {
    const edges = edgesByIndex.get(candidate.indexKey) ?? new Set<string>();
    edges.add(candidate.objectKey);
    edgesByIndex.set(candidate.indexKey, edges);
    const edgeKey = JSON.stringify([
      candidate.indexKey,
      candidate.objectKey,
    ]);
    const current = candidateByEdge.get(edgeKey);
    if (
      current === undefined ||
      compareStrings(candidate.nodeId, current.nodeId) < 0
    ) {
      candidateByEdge.set(edgeKey, candidate);
    }
  }
  for (const edges of edgesByIndex.values()) {
    const sorted = [...edges].sort(compareStrings);
    edges.clear();
    for (const edge of sorted) edges.add(edge);
  }

  const objectToIndex = new Map<string, string>();
  const augment = (indexKey: string, seenObjects: Set<string>): boolean => {
    const edges = edgesByIndex.get(indexKey);
    if (edges === undefined) return false;
    for (const objectKey of edges) {
      if (seenObjects.has(objectKey)) continue;
      seenObjects.add(objectKey);
      const occupyingIndex = objectToIndex.get(objectKey);
      if (
        occupyingIndex === undefined ||
        augment(occupyingIndex, seenObjects)
      ) {
        objectToIndex.set(objectKey, indexKey);
        return true;
      }
    }
    return false;
  };

  let matched = 0;
  for (const indexKey of indexKeys) {
    if (augment(indexKey, new Set<string>())) {
      matched += 1;
      if (matched >= required) break;
    }
  }
  if (objectToIndex.size < required) return null;

  const pairs = [...objectToIndex.entries()]
    .map(([objectKey, indexKey]) => {
      const candidate = candidateByEdge.get(
        JSON.stringify([indexKey, objectKey]),
      );
      if (candidate === undefined) {
        throw new Error("threshold matching produced an invalid edge");
      }
      return candidate;
    })
    .sort((left, right) => {
      return (
        compareStrings(left.indexKey, right.indexKey) ||
        compareStrings(left.objectKey, right.objectKey)
      );
    });
  return pairs.slice(0, required);
}

function findThresholdMatch(
  model: SnapshotModel,
  spec: ThresholdSpec,
  recovered: ReadonlySet<NodeId>,
): ThresholdMatch | null {
  const candidates = shardCandidates(model, spec, recovered);
  const byRound = new Map<string, ShardCandidate[]>();
  for (const candidate of candidates) {
    const roundCandidates = byRound.get(candidate.round) ?? [];
    roundCandidates.push(candidate);
    byRound.set(candidate.round, roundCandidates);
  }
  const rounds = [...byRound.keys()].sort(compareStrings);
  for (const round of rounds) {
    const matched = matchThreshold(byRound.get(round) ?? [], spec.required);
    if (matched !== null) {
      return {
        round,
        shards: matched.map((candidate) => ({
          nodeId: candidate.nodeId,
          round: candidate.round,
          index: candidate.index,
          objectId: candidate.displayObjectId,
        })),
      };
    }
  }
  return null;
}

function proofForNode(
  model: SnapshotModel,
  node: KeyNode,
  recovered: ReadonlySet<NodeId>,
): InternalProof | null {
  switch (node.kind) {
    case "material":
      return { kind: "material", uses: [] };
    case "stored":
      return { kind: "stored", uses: [] };
    case "derived": {
      const inputs = [...new Set(node.inputs)].sort(compareStrings);
      if (inputs.every((input) => recovered.has(input))) {
        return { kind: "derived", uses: inputs };
      }
      return null;
    }
    case "sealed": {
      const wrappers = [...new Set(node.wrappingKeys)].sort(compareStrings);
      if (
        recovered.has(node.backup) &&
        wrappers.every((wrapper) => recovered.has(wrapper))
      ) {
        return { kind: "sealed", uses: [node.backup, ...wrappers] };
      }
      return null;
    }
    case "threshold": {
      const match = findThresholdMatch(model, node.threshold, recovered);
      if (match === null) return null;
      return {
        kind: "threshold",
        uses: match.shards.map((shard) => shard.nodeId),
        shards: match.shards,
      };
    }
    case "alias":
      return recovered.has(node.source)
        ? { kind: "alias", uses: [node.source] }
        : null;
    case "export":
      return { kind: "export", uses: [] };
  }
}

function evaluateInternal(
  model: SnapshotModel,
  requestedDeletedObjectIds: readonly string[],
): InternalEvaluation {
  const deletedObjectIds = [...new Set(requestedDeletedObjectIds)].sort(
    compareStrings,
  );
  const deleted = new Set(deletedObjectIds);
  const recovered = new Set<NodeId>();
  const proofs = new Map<NodeId, InternalProof>();

  for (const nodeId of model.sortedNodeIds) {
    const copies = model.exportedCopiesByNode.get(nodeId);
    if (copies === undefined || copies.length === 0) continue;
    recovered.add(nodeId);
    proofs.set(nodeId, {
      kind: "export",
      uses: [],
      copyIds: copies.map((copy) => copy.id),
    });
  }

  let changed = true;
  while (changed) {
    changed = false;
    for (const nodeId of model.sortedNodeIds) {
      if (recovered.has(nodeId)) continue;
      const node = model.nodesById.get(nodeId);
      if (node === undefined) continue;
      if (node.objectId !== undefined && deleted.has(node.objectId)) continue;
      const proof = proofForNode(model, node, recovered);
      if (proof === null) continue;
      recovered.add(nodeId);
      proofs.set(nodeId, { ...proof, objectId: node.objectId });
      changed = true;
    }
  }

  return {
    model,
    deletedObjectIds,
    recovered,
    proofs,
  };
}

function buildWitness(
  evaluation: InternalEvaluation,
  nodeId: NodeId,
): RecoveryWitness | null {
  if (!evaluation.proofs.has(nodeId)) return null;
  const steps: RecoveryStep[] = [];
  const copyIds: string[] = [];
  const visiting = new Set<NodeId>();
  const emitted = new Set<NodeId>();

  const visit = (currentNodeId: NodeId): void => {
    if (emitted.has(currentNodeId) || visiting.has(currentNodeId)) return;
    const proof = evaluation.proofs.get(currentNodeId);
    if (proof === undefined) return;
    visiting.add(currentNodeId);
    for (const dependency of proof.uses) visit(dependency);
    visiting.delete(currentNodeId);
    emitted.add(currentNodeId);
    if (proof.copyIds !== undefined) appendUnique(copyIds, proof.copyIds);
    const step: RecoveryStep = {
      nodeId: currentNodeId,
      kind: proof.kind,
      uses: [...proof.uses],
      ...(proof.objectId === undefined ? {} : { objectId: proof.objectId }),
      ...(proof.shards === undefined ? {} : { shards: proof.shards }),
      ...(proof.copyIds === undefined ? {} : { copyIds: proof.copyIds }),
    };
    steps.push(step);
  };

  visit(nodeId);
  copyIds.sort(compareStrings);
  return { nodeId, steps, copyIds };
}

function makeEvaluation(evaluation: InternalEvaluation): RecoveryEvaluation {
  return {
    deletedObjectIds: evaluation.deletedObjectIds,
    recoveredNodeIds: [...evaluation.recovered].sort(compareStrings),
    recovered: evaluation.recovered,
    witnessFor: (nodeId) => buildWitness(evaluation, nodeId),
  };
}

export function evaluateRecovery(
  snapshot: KeySnapshot,
  deletedObjectIds: readonly StorageObjectId[] = [],
): RecoveryEvaluation {
  const issues = validateSnapshot(snapshot);
  if (issues.length > 0) throw snapshotError(issues);
  const model = buildModel(snapshot);
  for (const objectId of deletedObjectIds) {
    if (!model.objectIds.has(objectId)) {
      throw new Error(`unknown storage object id: ${objectId}`);
    }
  }
  return makeEvaluation(evaluateInternal(model, deletedObjectIds));
}

function mandatoryFailures(
  before: InternalEvaluation,
  after: InternalEvaluation,
): MandatoryFailure[] {
  return after.model.mandatoryNodeIds
    .filter((keyId) => !after.recovered.has(keyId))
    .map((keyId) => ({
      keyId,
      recoverableBeforeDeletion: before.recovered.has(keyId),
      witnessBeforeDeletion: buildWitness(before, keyId),
    }));
}

function mandatoryWitnesses(
  evaluation: InternalEvaluation,
): Readonly<Record<NodeId, RecoveryWitness>> {
  return Object.fromEntries(
    evaluation.model.mandatoryNodeIds.map((keyId): [NodeId, RecoveryWitness] => {
      const witness = buildWitness(evaluation, keyId);
      if (witness === null) {
        throw new Error(`mandatory key ${keyId} has no recovery witness`);
      }
      return [keyId, witness];
    }),
  );
}

function exportsInWitness(witness: RecoveryWitness | null): string[] {
  const copies: string[] = [];
  if (witness === null) return copies;
  for (const step of witness.steps) {
    if (step.kind !== "export") continue;
    if (step.copyIds !== undefined && step.copyIds.length > 0) {
      appendUnique(copies, step.copyIds);
    } else {
      appendUnique(copies, [step.nodeId]);
    }
  }
  return copies.sort(compareStrings);
}

function* combinations(
  values: readonly string[],
  size: number,
  start = 0,
  prefix: string[] = [],
): Generator<string[]> {
  if (prefix.length === size) {
    yield [...prefix];
    return;
  }
  const remaining = size - prefix.length;
  for (let index = start; index <= values.length - remaining; index += 1) {
    prefix.push(values[index]);
    yield* combinations(values, size, index + 1, prefix);
    prefix.pop();
  }
}

function isAcceptableDeletion(
  model: SnapshotModel,
  deletedObjectIds: readonly string[],
): boolean {
  const evaluation = evaluateInternal(model, deletedObjectIds);
  if (evaluation.recovered.has(model.snapshot.targetNodeId)) return false;
  return model.mandatoryNodeIds.every((keyId) =>
    evaluation.recovered.has(keyId),
  );
}

function firstValidDeletionSet(
  model: SnapshotModel,
  size: number,
): string[] | null {
  for (const candidate of combinations(model.sortedObjectIds, size)) {
    if (isAcceptableDeletion(model, candidate)) return candidate;
  }
  return null;
}

export function planDestruction(snapshot: KeySnapshot): DestructionPlan {
  const issues = validateSnapshot(snapshot);
  if (issues.length > 0) {
    return {
      status: "invalid-snapshot",
      targetNodeId:
        isRecord(snapshot) && isNonEmptyString(snapshot.targetNodeId)
          ? snapshot.targetNodeId
          : null,
      deletedObjectIds: null,
      issues,
    };
  }

  const model = buildModel(snapshot);
  const before = evaluateInternal(model, []);
  const initialMandatoryFailures = mandatoryFailures(before, before);
  if (initialMandatoryFailures.length > 0) {
    return {
      status: "impossible",
      targetNodeId: snapshot.targetNodeId,
      deletedObjectIds: null,
      code: "preservation-constraint",
      message:
        "At least one key that must remain recoverable is already unrecoverable.",
      targetRecoverableBefore: before.recovered.has(snapshot.targetNodeId),
      exportedCopyIds: [],
      blockingMandatoryKeys: initialMandatoryFailures,
      allObjectsDeletedTargetWitness: null,
    };
  }

  if (!before.recovered.has(snapshot.targetNodeId)) {
    return {
      status: "already-unrecoverable",
      targetNodeId: snapshot.targetNodeId,
      deletedObjectIds: [],
      targetRecoverableBefore: false,
      targetRecoverableAfter: false,
      targetWitnessBefore: null,
      mandatoryWitnesses: mandatoryWitnesses(before),
    };
  }

  const allDeleted = evaluateInternal(model, model.sortedObjectIds);
  const targetSurvivesEverything = allDeleted.recovered.has(
    snapshot.targetNodeId,
  );
  if (targetSurvivesEverything) {
    const witness = buildWitness(allDeleted, snapshot.targetNodeId);
    const exportedCopyIds = exportsInWitness(witness);
    if (exportedCopyIds.length > 0) {
      return {
        status: "impossible",
        targetNodeId: snapshot.targetNodeId,
        deletedObjectIds: null,
        code: "exported-copy",
        message:
          "The target remains recoverable from an already exported copy, independent of deletable storage.",
        targetRecoverableBefore: true,
        exportedCopyIds,
        blockingMandatoryKeys: mandatoryFailures(before, allDeleted),
        allObjectsDeletedTargetWitness: witness,
      };
    }
    return {
      status: "impossible",
      targetNodeId: snapshot.targetNodeId,
      deletedObjectIds: null,
      code: "intrinsic-recovery",
      message:
        "The target remains recoverable from non-deletable key material even after every storage object is deleted.",
      targetRecoverableBefore: true,
      exportedCopyIds: [],
      blockingMandatoryKeys: mandatoryFailures(before, allDeleted),
      allObjectsDeletedTargetWitness: witness,
    };
  }

  for (let size = 1; size <= model.sortedObjectIds.length; size += 1) {
    const candidate = firstValidDeletionSet(model, size);
    if (candidate === null) continue;
    const after = evaluateInternal(model, candidate);
    const targetWitnessBefore = buildWitness(before, snapshot.targetNodeId);
    if (targetWitnessBefore === null) {
      throw new Error("target recovery witness is missing");
    }
    return {
      status: "planned",
      targetNodeId: snapshot.targetNodeId,
      deletedObjectIds: candidate,
      targetRecoverableBefore: true,
      targetRecoverableAfter: false,
      targetWitnessBefore,
      mandatoryWitnesses: mandatoryWitnesses(after),
    };
  }

  return {
    status: "impossible",
    targetNodeId: snapshot.targetNodeId,
    deletedObjectIds: null,
    code: "preservation-constraint",
    message:
      "Every deletion set that makes the target unrecoverable violates a preservation constraint.",
    targetRecoverableBefore: true,
    exportedCopyIds: [],
    blockingMandatoryKeys: mandatoryFailures(before, allDeleted),
    allObjectsDeletedTargetWitness: buildWitness(
      allDeleted,
      snapshot.targetNodeId,
    ),
  };
}

export function findMinimumDestructionSet(
  snapshot: KeySnapshot,
): readonly StorageObjectId[] | null {
  const plan = planDestruction(snapshot);
  if (plan.status === "planned" || plan.status === "already-unrecoverable") {
    return plan.deletedObjectIds;
  }
  return null;
}

function invalidCertificate(
  message: string,
  code: CertificateStatusCode,
  identity: {
    readonly deletedObjectIds: readonly string[];
    readonly targetRecoverableBefore?: boolean | null;
    readonly targetRecoverableAfter?: boolean | null;
    readonly targetWitnessBefore?: RecoveryWitness | null;
    readonly targetWitnessAfter?: RecoveryWitness | null;
    readonly restoreWitnesses?: readonly RestoreWitness[];
    readonly mandatoryFailures?: readonly MandatoryFailure[];
    readonly exportedCopyIds?: readonly string[];
    readonly issues?: readonly string[];
  } = { deletedObjectIds: [] },
): CertificateVerification {
  return {
    valid: false,
    status: "rejected",
    code,
    message,
    deletedObjectIds: identity.deletedObjectIds,
    targetRecoverableBefore: identity.targetRecoverableBefore ?? null,
    targetRecoverableAfter: identity.targetRecoverableAfter ?? null,
    targetWitnessBefore: identity.targetWitnessBefore ?? null,
    targetWitnessAfter: identity.targetWitnessAfter ?? null,
    restoreWitnesses: identity.restoreWitnesses ?? [],
    mandatoryFailures: identity.mandatoryFailures ?? [],
    exportedCopyIds: identity.exportedCopyIds ?? [],
    issues: identity.issues ?? [],
  };
}

export function verifyDestructionCertificate(
  snapshot: KeySnapshot,
  certificate: DestructionCertificate | readonly StorageObjectId[],
): CertificateVerification {
  const issues = validateSnapshot(snapshot);
  if (issues.length > 0) {
    return invalidCertificate(
      "The snapshot is invalid.",
      "invalid-snapshot",
      { deletedObjectIds: [], issues },
    );
  }

  const model = buildModel(snapshot);
  if (
    !Array.isArray(certificate) &&
    (!isRecord(certificate) ||
      !Array.isArray(certificate.deletedObjectIds))
  ) {
    return invalidCertificate(
      "The destruction certificate is malformed.",
      "invalid-certificate",
      {
        deletedObjectIds: [],
        issues: ["certificate must contain a deletedObjectIds array"],
      },
    );
  }
  const certificateObject: DestructionCertificate = Array.isArray(certificate)
    ? { deletedObjectIds: certificate }
    : (certificate as DestructionCertificate);
  const certificateIssues: string[] = [];

  if (
    certificateObject.targetNodeId !== undefined &&
    certificateObject.targetNodeId !== snapshot.targetNodeId
  ) {
    certificateIssues.push(
      `certificate target ${certificateObject.targetNodeId} does not match snapshot target ${snapshot.targetNodeId}`,
    );
  }
  if (!Array.isArray(certificateObject.deletedObjectIds)) {
    certificateIssues.push("certificate deletedObjectIds must be an array");
  }
  const rawDeleted = Array.isArray(certificateObject.deletedObjectIds)
    ? certificateObject.deletedObjectIds
    : [];
  const seenDeleted = new Set<string>();
  for (const objectId of rawDeleted) {
    if (!isNonEmptyString(objectId)) {
      certificateIssues.push("certificate contains a non-string object id");
      continue;
    }
    if (seenDeleted.has(objectId)) {
      certificateIssues.push(`certificate repeats object id ${objectId}`);
    }
    seenDeleted.add(objectId);
    if (!model.objectIds.has(objectId)) {
      certificateIssues.push(`certificate references unknown object ${objectId}`);
    }
  }
  if (certificateIssues.length > 0) {
    return invalidCertificate(
      "The destruction certificate is malformed.",
      "invalid-certificate",
      {
        deletedObjectIds: [...seenDeleted].sort(compareStrings),
        issues: certificateIssues,
      },
    );
  }

  const deletedObjectIds = [...seenDeleted].sort(compareStrings);
  const before = evaluateInternal(model, []);
  const after = evaluateInternal(model, deletedObjectIds);
  const targetBefore = before.recovered.has(snapshot.targetNodeId);
  const targetAfter = after.recovered.has(snapshot.targetNodeId);
  const targetWitnessBefore = buildWitness(before, snapshot.targetNodeId);
  const targetWitnessAfter = buildWitness(after, snapshot.targetNodeId);
  const failuresAfter = mandatoryFailures(before, after);

  if (!targetBefore) {
    if (failuresAfter.length > 0) {
      return invalidCertificate(
        "The certificate cannot preserve every key that must remain recoverable.",
        "preservation-constraint",
        {
          deletedObjectIds,
          targetRecoverableBefore: false,
          targetRecoverableAfter: targetAfter,
          targetWitnessBefore,
          targetWitnessAfter,
          mandatoryFailures: failuresAfter,
        },
      );
    }
    if (deletedObjectIds.length > 0) {
      return invalidCertificate(
        "The target was already unrecoverable before deletion, so the only minimum certificate is the empty set.",
        "target-already-unrecoverable",
        {
          deletedObjectIds,
          targetRecoverableBefore: false,
          targetRecoverableAfter: targetAfter,
          targetWitnessBefore,
          targetWitnessAfter,
        },
      );
    }
    return {
      valid: true,
      status: "already-unrecoverable",
      code: "already-unrecoverable",
      message:
        "The target was already unrecoverable and no object deletion is needed.",
      deletedObjectIds,
      targetRecoverableBefore: false,
      targetRecoverableAfter: false,
      targetWitnessBefore: null,
      targetWitnessAfter: null,
      restoreWitnesses: [],
      mandatoryFailures: [],
      exportedCopyIds: [],
      issues: [],
    };
  }

  if (targetAfter) {
    const allDeleted = evaluateInternal(model, model.sortedObjectIds);
    const allDeletedWitness = buildWitness(
      allDeleted,
      snapshot.targetNodeId,
    );
    const exportedCopyIds = exportsInWitness(allDeletedWitness);
    if (
      !allDeleted.recovered.has(snapshot.targetNodeId) ||
      exportedCopyIds.length === 0
    ) {
      return invalidCertificate(
        "The certificate does not make the target unrecoverable.",
        "insufficient-deletion",
        {
          deletedObjectIds,
          targetRecoverableBefore: true,
          targetRecoverableAfter: true,
          targetWitnessBefore,
          targetWitnessAfter,
        },
      );
    }
    return invalidCertificate(
      "The target remains recoverable from an already exported copy, independent of deletable storage.",
      "exported-copy",
      {
        deletedObjectIds,
        targetRecoverableBefore: true,
        targetRecoverableAfter: true,
        targetWitnessBefore,
        targetWitnessAfter,
        exportedCopyIds,
      },
    );
  }

  const restoreWitnesses: RestoreWitness[] = deletedObjectIds.map(
    (restoredObjectId) => {
      const remainingDeletedObjectIds = deletedObjectIds.filter(
        (objectId) => objectId !== restoredObjectId,
      );
      const restored = evaluateInternal(model, remainingDeletedObjectIds);
      const targetRecoverable = restored.recovered.has(
        snapshot.targetNodeId,
      );
      return {
        restoredObjectId,
        remainingDeletedObjectIds,
        targetRecoverable,
        targetWitness: targetRecoverable
          ? buildWitness(restored, snapshot.targetNodeId)
          : null,
      };
    },
  );

  if (failuresAfter.length > 0) {
    return invalidCertificate(
      "The certificate deletes an object that is needed by a key which must remain recoverable.",
      "preservation-constraint",
      {
        deletedObjectIds,
        targetRecoverableBefore: true,
        targetRecoverableAfter: false,
        targetWitnessBefore,
        targetWitnessAfter: null,
        restoreWitnesses,
        mandatoryFailures: failuresAfter,
      },
    );
  }

  if (restoreWitnesses.some((witness) => !witness.targetRecoverable)) {
    return invalidCertificate(
      "The certificate is not inclusion-minimal: restoring a single deleted object still leaves the target unrecoverable.",
      "not-minimal",
      {
        deletedObjectIds,
        targetRecoverableBefore: true,
        targetRecoverableAfter: false,
        targetWitnessBefore,
        restoreWitnesses,
      },
    );
  }

  for (let size = 0; size < deletedObjectIds.length; size += 1) {
    const smaller = firstValidDeletionSet(model, size);
    if (smaller !== null) {
      return invalidCertificate(
        "The certificate is not cardinality-minimal.",
        "not-minimal",
        {
          deletedObjectIds,
          targetRecoverableBefore: true,
          targetRecoverableAfter: false,
          targetWitnessBefore,
          restoreWitnesses,
        },
      );
    }
  }

  for (const candidate of combinations(
    model.sortedObjectIds,
    deletedObjectIds.length,
  )) {
    if (compareStringArrays(candidate, deletedObjectIds) >= 0) break;
    if (isAcceptableDeletion(model, candidate)) {
      return invalidCertificate(
        "The certificate is valid but not the lexicographically smallest minimum deletion set.",
        "not-lexicographically-minimal",
        {
          deletedObjectIds,
          targetRecoverableBefore: true,
          targetRecoverableAfter: false,
          targetWitnessBefore,
          restoreWitnesses,
        },
      );
    }
  }

  return {
    valid: true,
    status: "certified",
    code: "certified",
    message:
      "The target is unrecoverable after deletion, all preservation constraints hold, and the deletion set is the lexicographically smallest minimum.",
    deletedObjectIds,
    targetRecoverableBefore: true,
    targetRecoverableAfter: false,
    targetWitnessBefore,
    targetWitnessAfter: null,
    restoreWitnesses,
    mandatoryFailures: [],
    exportedCopyIds: [],
    issues: [],
  };
}

export const checkDestructionCertificate = verifyDestructionCertificate;
