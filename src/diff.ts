import type {
  DiffReport,
  GrantGroup,
  Resource,
  ResourceTypeDiff,
  Snapshot,
  SourceChain,
  UserDiff,
} from "./types.ts";
import {
  computeEffectiveGrants,
  indexSnapshot,
  type EffectiveGrant,
} from "./effective.ts";
import { compressPaths } from "./compress.ts";

/**
 * Diff two snapshots (before / after migrating users to a new org tree).
 *
 * Both sides are recomputed from scratch, so:
 * - a renamed org (same id, new name) yields identical effective grants
 *   and therefore shows up as "unchanged", never as a change;
 * - a deleted exception simply has no effect on the new side, so any
 *   permission it granted or blocked is correctly reported as lost/added.
 */
export function diffSnapshots(
  before: Snapshot,
  after: Snapshot,
  catalog: Resource[],
): DiffReport {
  const beforeIndex = indexSnapshot(before);
  const afterIndex = indexSnapshot(after);
  const beforeGrants = computeEffectiveGrants(beforeIndex, catalog);
  const afterGrants = computeEffectiveGrants(afterIndex, catalog);

  const catalogPathsByType = new Map<string, string[]>();
  for (const resource of catalog) {
    const list = catalogPathsByType.get(resource.type) ?? [];
    list.push(resource.path);
    catalogPathsByType.set(resource.type, list);
  }

  const userIds = [
    ...new Set([...beforeGrants.keys(), ...afterGrants.keys()]),
  ].sort();

  const users: UserDiff[] = [];
  for (const userId of userIds) {
    users.push(
      diffUser(
        userId,
        beforeGrants.get(userId) ?? [],
        afterGrants.get(userId) ?? [],
        catalogPathsByType,
      ),
    );
  }
  return { users };
}

function grantKey(grant: EffectiveGrant): string {
  return grant.action + "" + grant.path;
}

function sourceKey(source: SourceChain): string {
  return source.ruleKind + ":" + source.ruleId;
}

function diffUser(
  userId: string,
  beforeGrants: EffectiveGrant[],
  afterGrants: EffectiveGrant[],
  catalogPathsByType: Map<string, string[]>,
): UserDiff {
  const beforeByKey = new Map(beforeGrants.map((g) => [grantKey(g), g]));
  const afterByKey = new Map(afterGrants.map((g) => [grantKey(g), g]));

  const resourceTypes = [
    ...new Set([
      ...beforeGrants.map((g) => g.resourceType),
      ...afterGrants.map((g) => g.resourceType),
    ]),
  ].sort();

  const byResourceType: Record<string, ResourceTypeDiff> = {};
  let clean = true;

  for (const resourceType of resourceTypes) {
    const added: EffectiveGrant[] = [];
    const lost: EffectiveGrant[] = [];
    const unchanged: EffectiveGrant[] = [];

    for (const grant of afterGrants) {
      if (grant.resourceType !== resourceType) continue;
      if (beforeByKey.has(grantKey(grant))) unchanged.push(grant);
      else added.push(grant);
    }
    for (const grant of beforeGrants) {
      if (grant.resourceType !== resourceType) continue;
      if (!afterByKey.has(grantKey(grant))) lost.push(grant);
    }

    if (added.length > 0 || lost.length > 0) clean = false;
    if (added.length + lost.length + unchanged.length === 0) continue;

    const catalogPaths = catalogPathsByType.get(resourceType) ?? [];
    byResourceType[resourceType] = {
      added: groupAndCompress(added, catalogPaths),
      lost: groupAndCompress(lost, catalogPaths),
      unchanged: groupAndCompress(unchanged, catalogPaths),
    };
  }

  return { userId, byResourceType, clean };
}

/**
 * Group grants by (action, winning rule) and compress each group's paths.
 * Grouping by rule keeps every compressed group explainable by exactly one
 * shortest source chain.
 */
function groupAndCompress(
  grants: EffectiveGrant[],
  catalogPaths: string[],
): GrantGroup[] {
  const groups = new Map<
    string,
    { action: string; source: SourceChain; paths: string[] }
  >();
  for (const grant of grants) {
    const key = grant.action + "" + sourceKey(grant.source);
    const group = groups.get(key) ?? {
      action: grant.action,
      source: grant.source,
      paths: [],
    };
    group.paths.push(grant.path);
    groups.set(key, group);
  }

  return [...groups.values()]
    .map((group) => ({
      action: group.action,
      paths: compressPaths(group.paths, catalogPaths),
      source: group.source,
    }))
    .sort((a, b) =>
      (a.action + a.paths.join(",")).localeCompare(
        b.action + b.paths.join(","),
      ),
    );
}
