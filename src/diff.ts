/**
 * 快照差分：对每名用户、每类资源、每个动作，
 * 计算新增 / 失去 / 不变的权限，路径压缩表示，并附最短来源链。
 */
import { compressPaths, patternCovers } from './paths.ts';
import { computeEffective, denyChainFor, keyOf, shortestChain } from './effective.ts';
import type {
  ActionDiff,
  DiffReport,
  Snapshot,
  SourceChain,
  SourcedPath,
  UserResourceDiff,
} from './types.ts';

/** 把具体路径集合压缩，并为每个压缩模式附上成员中的最短来源链。 */
function compressWithChains(
  paths: ReadonlySet<string>,
  chainOf: (path: string) => SourceChain | undefined,
  catalogPaths: readonly string[],
): SourcedPath[] {
  const patterns = compressPaths(paths, catalogPaths);
  const byPattern = new Map<string, SourceChain[]>();
  for (const p of paths) {
    const pattern = patterns.find((pat) => patternCovers(pat, p));
    if (!pattern) continue;
    const chain = chainOf(p);
    if (!chain) continue;
    const list = byPattern.get(pattern) ?? [];
    list.push(chain);
    byPattern.set(pattern, list);
  }
  const out: SourcedPath[] = [];
  for (const pattern of patterns) {
    const chain = shortestChain(byPattern.get(pattern) ?? []) ?? [];
    out.push({ path: pattern, chain });
  }
  return out;
}

export function diffSnapshots(before: Snapshot, after: Snapshot): DiffReport {
  const effBefore = computeEffective(before);
  const effAfter = computeEffective(after);

  const userIds = [...new Set([...effBefore.keys(), ...effAfter.keys()])].sort();
  const users: UserResourceDiff[] = [];

  for (const userId of userIds) {
    const b = effBefore.get(userId);
    const a = effAfter.get(userId);
    const keys = new Set<string>([...(b?.byKey.keys() ?? []), ...(a?.byKey.keys() ?? [])]);

    // 按 resourceType 聚合
    const byType = new Map<string, ActionDiff[]>();
    for (const key of [...keys].sort()) {
      const [resourceType, action] = key.split(' ');
      const beforeMap = b?.byKey.get(key) ?? new Map<string, SourceChain>();
      const afterMap = a?.byKey.get(key) ?? new Map<string, SourceChain>();

      const addedPaths = new Set<string>();
      const lostPaths = new Set<string>();
      const unchangedPaths = new Set<string>();
      for (const p of afterMap.keys()) {
        if (beforeMap.has(p)) unchangedPaths.add(p);
        else addedPaths.add(p);
      }
      for (const p of beforeMap.keys()) {
        if (!afterMap.has(p)) lostPaths.add(p);
      }

      const afterCatalog = after.catalog[resourceType] ?? [];
      const beforeCatalog = before.catalog[resourceType] ?? [];

      const diff: ActionDiff = {
        action,
        added: compressWithChains(addedPaths, (p) => afterMap.get(p), afterCatalog),
        // 失去：优先指向 after 快照中生效的 deny 例外，否则指向 before 中原来的来源
        lost: compressWithChains(
          lostPaths,
          (p) => denyChainFor(after, userId, resourceType, action, p) ?? beforeMap.get(p),
          beforeCatalog,
        ),
        unchanged: compressWithChains(unchangedPaths, (p) => afterMap.get(p), afterCatalog),
      };

      const list = byType.get(resourceType) ?? [];
      list.push(diff);
      byType.set(resourceType, list);
    }

    for (const [resourceType, actions] of [...byType.entries()].sort()) {
      const hasChanges = actions.some((d) => d.added.length > 0 || d.lost.length > 0);
      users.push({ userId, resourceType, actions, hasChanges });
    }
  }

  return { users };
}

/** 渲染来源链为可读字符串，如 `u1 -> org:root(总部) -> grant:g1`。 */
export function formatChain(chain: SourceChain): string {
  return chain.map((l) => (l.kind === 'user' ? l.id : `${l.kind}:${l.id}(${l.label})`)).join(' -> ');
}
