/**
 * 有效权限计算：祖先授权并集 + 用户显式例外（allow 增补 / deny 剔除），
 * 并为每条具体权限记录最短来源链。
 */
import type {
  ChainLink,
  Grant,
  OrgNode,
  Snapshot,
  SourceChain,
  User,
  UserException,
} from './types.ts';
import { expandPrefix, prefixCovers } from './paths.ts';

export interface EffectiveSet {
  /** key: `${resourceType} ${action}`，value: 具体路径 -> 最短来源链 */
  byKey: Map<string, Map<string, SourceChain>>;
  /** deny 例外（用于解释“失去”的来源） */
  denies: UserException[];
  user: User;
}

export function keyOf(resourceType: string, action: string): string {
  return resourceType + ' ' + action;
}

/** 从用户所在组织一路向上到根的组织链（含自身，索引 0 为自身）。 */
export function orgChainOf(snapshot: Snapshot, orgId: string): OrgNode[] {
  const chain: OrgNode[] = [];
  let current = snapshot.tree.nodes.get(orgId);
  if (!current) throw new Error(`未知组织节点: ${orgId}`);
  while (current) {
    chain.push(current);
    current = current.parentId ? snapshot.tree.nodes.get(current.parentId) : undefined;
    if (current === undefined && chain[chain.length - 1].parentId) {
      throw new Error(`组织 ${chain[chain.length - 1].id} 的父节点缺失`);
    }
  }
  return chain;
}

function chainLess(a: SourceChain, b: SourceChain): boolean {
  if (a.length !== b.length) return a.length < b.length;
  const ka = a.map((l) => `${l.kind}:${l.id}`).join('>');
  const kb = b.map((l) => `${l.kind}:${l.id}`).join('>');
  return ka < kb;
}

/** 在候选链中取最短的一条（等长时取字典序最小，保证确定性）。 */
export function shortestChain(chains: Iterable<SourceChain>): SourceChain | undefined {
  let best: SourceChain | undefined;
  for (const c of chains) {
    if (!best || chainLess(c, best)) best = c;
  }
  return best;
}

function grantChain(user: User, orgPath: OrgNode[], grant: Grant): SourceChain {
  const links: ChainLink[] = [{ kind: 'user', id: user.id, label: user.id }];
  for (const org of orgPath) links.push({ kind: 'org', id: org.id, label: org.name });
  links.push({ kind: 'grant', id: grant.id, label: grant.id });
  return links;
}

function exceptionChain(user: User, ex: UserException): SourceChain {
  return [
    { kind: 'user', id: user.id, label: user.id },
    { kind: 'exception', id: ex.id, label: ex.id },
  ];
}

/** 计算单个快照中全部用户的有效权限。 */
export function computeEffective(snapshot: Snapshot): Map<string, EffectiveSet> {
  const grantsByOrg = new Map<string, Grant[]>();
  for (const g of snapshot.grants) {
    const list = grantsByOrg.get(g.orgId) ?? [];
    list.push(g);
    grantsByOrg.set(g.orgId, list);
  }
  const exceptionsByUser = new Map<string, UserException[]>();
  for (const ex of snapshot.exceptions) {
    const list = exceptionsByUser.get(ex.userId) ?? [];
    list.push(ex);
    exceptionsByUser.set(ex.userId, list);
  }

  const result = new Map<string, EffectiveSet>();
  for (const user of snapshot.users) {
    const orgs = orgChainOf(snapshot, user.orgId);
    const exceptions = exceptionsByUser.get(user.id) ?? [];
    const denies = exceptions.filter((e) => e.effect === 'deny');
    const allows = exceptions.filter((e) => e.effect === 'allow');

    // 收集所有相关的 (resourceType, action) 键
    const keys = new Set<string>();
    for (const g of snapshot.grants) for (const a of g.actions) keys.add(keyOf(g.resourceType, a));
    for (const e of exceptions) for (const a of e.actions) keys.add(keyOf(e.resourceType, a));

    const byKey = new Map<string, Map<string, SourceChain>>();
    for (const key of keys) {
      const [resourceType, action] = key.split(' ');
      const catalogPaths = snapshot.catalog[resourceType] ?? [];

      // 1) 继承：所有祖先组织授权的并集
      const inherited = new Map<string, SourceChain[]>();
      for (let depth = 0; depth < orgs.length; depth++) {
        const org = orgs[depth];
        for (const g of grantsByOrg.get(org.id) ?? []) {
          if (g.resourceType !== resourceType || !g.actions.includes(action)) continue;
          const orgPath = orgs.slice(0, depth + 1);
          for (const p of expandPrefix(g.path, catalogPaths)) {
            const list = inherited.get(p) ?? [];
            list.push(grantChain(user, orgPath, g));
            inherited.set(p, list);
          }
        }
      }

      // 2) deny 例外：从继承结果中剔除
      for (const ex of denies) {
        if (ex.resourceType !== resourceType || !ex.actions.includes(action)) continue;
        for (const p of expandPrefix(ex.path, catalogPaths)) inherited.delete(p);
      }

      // 3) allow 例外：显式增补
      for (const ex of allows) {
        if (ex.resourceType !== resourceType || !ex.actions.includes(action)) continue;
        for (const p of expandPrefix(ex.path, catalogPaths)) {
          const list = inherited.get(p) ?? [];
          list.push(exceptionChain(user, ex));
          inherited.set(p, list);
        }
      }

      // 4) 每条路径取最短来源链
      const finalMap = new Map<string, SourceChain>();
      for (const [p, chains] of inherited) {
        const best = shortestChain(chains);
        if (best) finalMap.set(p, best);
      }
      if (finalMap.size > 0) byKey.set(key, finalMap);
    }

    result.set(user.id, { byKey, denies, user });
  }
  return result;
}

/** 在快照中查找覆盖某路径的 deny 例外链（用于解释权限失去的原因）。 */
export function denyChainFor(
  snapshot: Snapshot,
  userId: string,
  resourceType: string,
  action: string,
  path: string,
): SourceChain | undefined {
  const user = snapshot.users.find((u) => u.id === userId);
  if (!user) return undefined;
  const exs = snapshot.exceptions.filter(
    (e) =>
      e.userId === userId &&
      e.effect === 'deny' &&
      e.resourceType === resourceType &&
      e.actions.includes(action) &&
      prefixCovers(e.path, path),
  );
  if (exs.length === 0) return undefined;
  return shortestChain(exs.map((e) => exceptionChain(user, e)));
}
