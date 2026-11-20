import type {
  KeyNode,
  KeySnapshot,
  ShardRef,
  ThresholdSpec,
} from "../src/index.ts";

function compareStrings(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

function nodeMap(snapshot: KeySnapshot): Map<string, KeyNode> {
  return new Map(snapshot.nodes.map((node) => [node.id, node]));
}

function shardObjectKey(
  shard: ShardRef,
  nodes: ReadonlyMap<string, KeyNode>,
): string {
  const node = nodes.get(shard.nodeId);
  const objectId = shard.objectId ?? node?.objectId;
  return objectId === undefined
    ? `virtual:${shard.nodeId}`
    : `object:${objectId}`;
}

function choose<T>(
  values: readonly T[],
  size: number,
  start = 0,
  prefix: T[] = [],
): T[][] {
  if (prefix.length === size) return [[...prefix]];
  const combinations: T[][] = [];
  const remaining = size - prefix.length;
  for (let index = start; index <= values.length - remaining; index += 1) {
    prefix.push(values[index]);
    combinations.push(...choose(values, size, index + 1, prefix));
    prefix.pop();
  }
  return combinations;
}

function thresholdCanRecover(
  spec: ThresholdSpec,
  recovered: ReadonlySet<string>,
  nodes: ReadonlyMap<string, KeyNode>,
): boolean {
  if (spec.required < 1) return false;
  const candidates = spec.shards.filter(
    (shard) =>
      recovered.has(shard.nodeId) &&
      (spec.round === undefined || shard.round === spec.round),
  );
  const rounds = [...new Set(candidates.map((shard) => shard.round))];
  for (const round of rounds) {
    const roundShards = candidates.filter((shard) => shard.round === round);
    for (const group of choose(roundShards, spec.required)) {
      const indexes = new Set(group.map((shard) => String(shard.index)));
      const objectKeys = new Set(
        group.map((shard) => shardObjectKey(shard, nodes)),
      );
      if (
        indexes.size === spec.required &&
        objectKeys.size === spec.required
      ) {
        return true;
      }
    }
  }
  return false;
}

export function referenceRecovered(
  snapshot: KeySnapshot,
  deletedObjectIds: readonly string[],
): Set<string> {
  const deleted = new Set(deletedObjectIds);
  const nodes = nodeMap(snapshot);
  const recovered = new Set<string>();

  for (const copy of snapshot.exportedCopies ?? []) {
    recovered.add(copy.keyId);
  }

  let changed = true;
  while (changed) {
    changed = false;
    for (const node of snapshot.nodes) {
      if (recovered.has(node.id)) continue;
      if (node.objectId !== undefined && deleted.has(node.objectId)) continue;

      let canRecover = false;
      switch (node.kind) {
        case "material":
        case "stored":
        case "export":
          canRecover = true;
          break;
        case "derived":
          canRecover = node.inputs.every((input) => recovered.has(input));
          break;
        case "sealed":
          canRecover =
            recovered.has(node.backup) &&
            node.wrappingKeys.every((wrapper) => recovered.has(wrapper));
          break;
        case "alias":
          canRecover = recovered.has(node.source);
          break;
        case "threshold":
          canRecover = thresholdCanRecover(
            node.threshold,
            recovered,
            nodes,
          );
          break;
      }
      if (canRecover) {
        recovered.add(node.id);
        changed = true;
      }
    }
  }

  return recovered;
}

export type ReferencePlan =
  | { readonly kind: "planned"; readonly deletedObjectIds: readonly string[] }
  | { readonly kind: "already-unrecoverable" }
  | { readonly kind: "impossible" };

export function referencePlan(snapshot: KeySnapshot): ReferencePlan {
  const mandatory = snapshot.mustRemainRecoverable ?? [];
  const before = referenceRecovered(snapshot, []);
  if (!mandatory.every((keyId) => before.has(keyId))) {
    return { kind: "impossible" };
  }
  if (!before.has(snapshot.targetNodeId)) {
    return { kind: "already-unrecoverable" };
  }

  const objectIds = snapshot.objects.map((object) => object.id).sort(compareStrings);
  for (let size = 1; size <= objectIds.length; size += 1) {
    for (const candidate of choose(objectIds, size)) {
      const recovered = referenceRecovered(snapshot, candidate);
      if (!recovered.has(snapshot.targetNodeId)) {
        if (mandatory.every((keyId) => recovered.has(keyId))) {
          return { kind: "planned", deletedObjectIds: candidate };
        }
      }
    }
  }
  return { kind: "impossible" };
}
