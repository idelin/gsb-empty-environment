import type {
  OrgPolicy,
  Resource,
  Snapshot,
  SourceChain,
  UserException,
} from "./types.ts";
import { matchPattern, specificity } from "./pattern.ts";

/**
 * Effective-permission computation.
 *
 * Everything is recomputed from the raw snapshot (tree + policies +
 * exceptions). Because nothing is derived from a previous snapshot's
 * results, deleting an exception can never "leak": its influence simply
 * disappears and inherited rules surface again.
 *
 * Precedence for one (user, resource, action):
 *   1. User exceptions beat org policies.
 *   2. Otherwise the rule on the org node closest to the user wins
 *      (this is what makes the reported source chain the shortest one:
 *      no rule further away from the user can be the deciding rule).
 *   3. Ties on the same node: more specific path pattern wins.
 *   4. Remaining ties: deny wins (fail closed).
 */

export interface EffectiveGrant {
  resourceType: string;
  action: string;
  path: string;
  source: SourceChain;
}

interface Candidate {
  tier: number; // 0 = exception, 1 = org policy
  distance: number; // org hops from the user's node (0 = own node)
  specificity: number;
  effect: "allow" | "deny";
  ruleId: string;
  ruleKind: "policy" | "exception";
  ruleLabel: string;
  nodeNamesUpward: string[]; // granting node -> ... -> user's node
}

function compareCandidates(a: Candidate, b: Candidate): number {
  if (a.tier !== b.tier) return a.tier - b.tier;
  if (a.distance !== b.distance) return a.distance - b.distance;
  if (a.specificity !== b.specificity) return b.specificity - a.specificity;
  // deny wins final ties
  if (a.effect !== b.effect) return a.effect === "deny" ? -1 : 1;
  return a.ruleId.localeCompare(b.ruleId);
}

function toSourceChain(winner: Candidate, userId: string): SourceChain {
  // Shortest chain: winning rule -> granting node -> ... -> user's node -> user.
  const hops = [winner.ruleLabel, ...winner.nodeNamesUpward, `user:${userId}`];
  return { hops, ruleId: winner.ruleId, ruleKind: winner.ruleKind };
}

interface IndexedSnapshot {
  nodeById: Map<string, { id: string; name: string; parentId: string | null }>;
  nodeChainUpward: Map<string, { id: string; name: string }[]>; // nodeId -> [self..root]
  policiesByNode: Map<string, OrgPolicy[]>;
  exceptionsByUser: Map<string, UserException[]>;
  userNode: Map<string, string>;
  userIds: string[];
  actionsByType: Map<string, Set<string>>;
}

export function indexSnapshot(snapshot: Snapshot): IndexedSnapshot {
  const nodeById = new Map<
    string,
    { id: string; name: string; parentId: string | null }
  >();
  for (const node of snapshot.tree.nodes) nodeById.set(node.id, node);

  const nodeChainUpward = new Map<string, { id: string; name: string }[]>();
  const chainFor = (nodeId: string): { id: string; name: string }[] => {
    const cached = nodeChainUpward.get(nodeId);
    if (cached) return cached;
    const node = nodeById.get(nodeId);
    if (!node) return [];
    const rest = node.parentId ? chainFor(node.parentId) : [];
    const chain = [{ id: node.id, name: node.name }, ...rest];
    nodeChainUpward.set(nodeId, chain);
    return chain;
  };
  for (const node of snapshot.tree.nodes) chainFor(node.id);

  const policiesByNode = new Map<string, OrgPolicy[]>();
  for (const policy of snapshot.policies) {
    const list = policiesByNode.get(policy.nodeId) ?? [];
    list.push(policy);
    policiesByNode.set(policy.nodeId, list);
  }

  const exceptionsByUser = new Map<string, UserException[]>();
  for (const exception of snapshot.exceptions) {
    const list = exceptionsByUser.get(exception.userId) ?? [];
    list.push(exception);
    exceptionsByUser.set(exception.userId, list);
  }

  const userNode = new Map<string, string>();
  for (const member of snapshot.tree.members) {
    userNode.set(member.userId, member.nodeId);
  }

  const userIds = [
    ...new Set([
      ...snapshot.tree.members.map((m) => m.userId),
      ...snapshot.exceptions.map((e) => e.userId),
    ]),
  ].sort();

  const actionsByType = new Map<string, Set<string>>();
  const addAction = (resourceType: string, action: string) => {
    const set = actionsByType.get(resourceType) ?? new Set<string>();
    set.add(action);
    actionsByType.set(resourceType, set);
  };
  for (const policy of snapshot.policies)
    addAction(policy.resourceType, policy.action);
  for (const exception of snapshot.exceptions)
    addAction(exception.resourceType, exception.action);

  return {
    nodeById,
    nodeChainUpward,
    policiesByNode,
    exceptionsByUser,
    userNode,
    userIds,
    actionsByType,
  };
}

/** Compute every effective allow for every user in the snapshot. */
export function computeEffectiveGrants(
  index: IndexedSnapshot,
  catalog: Resource[],
): Map<string, EffectiveGrant[]> {
  const catalogByType = new Map<string, Resource[]>();
  for (const resource of catalog) {
    const list = catalogByType.get(resource.type) ?? [];
    list.push(resource);
    catalogByType.set(resource.type, list);
  }

  const result = new Map<string, EffectiveGrant[]>();

  for (const userId of index.userIds) {
    const grants: EffectiveGrant[] = [];
    const nodeId = index.userNode.get(userId);
    const chain = nodeId ? index.nodeChainUpward.get(nodeId) ?? [] : [];
    const exceptions = index.exceptionsByUser.get(userId) ?? [];

    for (const [resourceType, resources] of catalogByType) {
      const actions = index.actionsByType.get(resourceType);
      if (!actions) continue;

      for (const resource of resources) {
        for (const action of actions) {
          const candidates: Candidate[] = [];

          for (const exception of exceptions) {
            if (
              exception.resourceType === resourceType &&
              exception.action === action &&
              matchPattern(exception.pathPattern, resource.path)
            ) {
              candidates.push({
                tier: 0,
                distance: 0,
                specificity: specificity(exception.pathPattern),
                effect: exception.effect,
                ruleId: exception.id,
                ruleKind: "exception",
                ruleLabel: `exception:${exception.id}(${exception.effect} ${exception.action} ${exception.pathPattern})`,
                nodeNamesUpward: [],
              });
            }
          }

          for (let distance = 0; distance < chain.length; distance++) {
            const node = chain[distance];
            for (const policy of index.policiesByNode.get(node.id) ?? []) {
              if (
                policy.resourceType === resourceType &&
                policy.action === action &&
                matchPattern(policy.pathPattern, resource.path)
              ) {
                candidates.push({
                  tier: 1,
                  distance,
                  specificity: specificity(policy.pathPattern),
                  effect: policy.effect,
                  ruleId: policy.id,
                  ruleKind: "policy",
                  ruleLabel: `policy:${policy.id}(${policy.effect} ${policy.action} ${policy.pathPattern})`,
                  // granting node -> ... -> user's node
                  nodeNamesUpward: chain
                    .slice(0, distance + 1)
                    .map((n) => `org:${n.name}`)
                    .reverse(),
                });
              }
            }
          }

          if (candidates.length === 0) continue;
          candidates.sort(compareCandidates);
          const winner = candidates[0];
          if (winner.effect !== "allow") continue;

          grants.push({
            resourceType,
            action,
            path: resource.path,
            source: toSourceChain(winner, userId),
          });
        }
      }
    }

    result.set(userId, grants);
  }

  return result;
}
