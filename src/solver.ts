import { absolutePath, buildTree, effectiveGrants, resolveRef } from './model';
import {
  ExceptionEntry,
  Move,
  Plan,
  RefResolution,
  RefStatus,
  Scenario,
  Tree,
} from './types';

/** 候选方案枚举上限；超过则退化为坐标下降贪心（15 节点内几乎不会触发）。 */
const ENUMERATION_CAP = 1000000;

interface BreakerOption {
  breaker: string;
  destination: string;
}

interface Candidate {
  options: BreakerOption[];
  parentOf: Map<string, string | null>;
  exceptionCount: number;
  brokenRefCount: number;
}

export function planMigration(scenario: Scenario): Plan {
  const name = scenario.name ?? 'scenario';
  const problems: string[] = [];
  const before = buildTree(scenario.nodes, problems);
  if (!before) return infeasible(name, problems);

  for (const node of before.nodes.values()) {
    if (node.targetParent === undefined) continue;
    if (!before.nodes.has(node.targetParent)) {
      problems.push(`node ${node.id}: unknown targetParent ${node.targetParent}`);
    }
    if (node.targetParent === node.id) {
      problems.push(`node ${node.id}: cannot be its own parent`);
    }
    if (node.fixed && node.targetParent !== node.parentId) {
      problems.push(
        `node ${node.id} is fixed but targetParent ${node.targetParent} differs from current parent ${node.parentId}`,
      );
    }
  }
  const root = before.nodes.get(before.rootId)!;
  if (!root.fixed) problems.push(`root node ${root.id} must be fixed`);
  if (root.targetParent !== undefined) {
    problems.push(`root node ${root.id} cannot have a targetParent`);
  }
  if (problems.length > 0) return infeasible(name, problems);

  // 强制赋值：有目标的节点指向目标父节点，其余保持原父节点。
  const forced = new Map<string, string | null>();
  for (const node of before.nodes.values()) {
    forced.set(node.id, node.targetParent !== undefined ? node.targetParent : node.parentId);
  }

  const cycles = findCycles(before, forced);
  const breakerPool: string[][] = [];
  for (const cyc of cycles) {
    const breakers = cyc.filter((id) => {
      const n = before.nodes.get(id)!;
      return !n.fixed && n.targetParent === undefined;
    });
    if (breakers.length === 0) {
      problems.push(
        `unbreakable cycle [${cyc.join(' -> ')}]: every node is fixed or has a forced targetParent`,
      );
    }
    breakerPool.push(breakers);
  }
  if (problems.length > 0) return infeasible(name, problems);

  // 每个环的候选破环方案：可动节点 x 任意目的地，合法性由整树校验兜底。
  const allIds = [...before.nodes.keys()];
  const perCycleOptions: BreakerOption[][] = breakerPool.map((breakers) => {
    const options: BreakerOption[] = [];
    for (const breaker of breakers) {
      for (const destination of allIds) {
        if (destination !== breaker) options.push({ breaker, destination });
      }
    }
    options.sort((a, b) =>
      (a.breaker + '>' + a.destination).localeCompare(b.breaker + '>' + b.destination),
    );
    return options;
  });

  const best = searchBestCandidate(before, forced, perCycleOptions);
  if (!best) {
    problems.push('no valid assignment found (all candidates produce cycles)');
    return infeasible(name, problems);
  }

  const after = applyParents(before, best.parentOf);
  const exceptions = computeExceptions(before, after);
  const refResolutions = computeRefResolutions(before, after);

  const moves: Move[] = [];
  for (const node of before.nodes.values()) {
    if (node.targetParent !== undefined && node.targetParent !== node.parentId) {
      moves.push({ id: node.id, from: node.parentId, to: node.targetParent, reason: 'target' });
    }
  }
  for (const opt of best.options) {
    moves.push({
      id: opt.breaker,
      from: before.nodes.get(opt.breaker)!.parentId,
      to: opt.destination,
      reason: 'cycle-break',
    });
  }
  moves.sort((a, b) => a.id.localeCompare(b.id));

  const beforeIds = [...before.nodes.keys()].sort();
  const afterIds = [...after.nodes.keys()].sort();
  const identityPreserved =
    beforeIds.length === afterIds.length && beforeIds.every((id, i) => id === afterIds[i]);

  return {
    scenario: name,
    feasible: true,
    problems: [],
    moves,
    moveCount: moves.length,
    exceptions,
    exceptionCount: exceptions.length,
    refResolutions,
    identityPreserved,
  };
}

function infeasible(name: string, problems: string[]): Plan {
  return {
    scenario: name,
    feasible: false,
    problems,
    moves: [],
    moveCount: 0,
    exceptions: [],
    exceptionCount: 0,
    refResolutions: [],
    identityPreserved: false,
  };
}

/** 函数图（每点出度为 1）环检测，环两两不相交。 */
function findCycles(tree: Tree, parentOf: Map<string, string | null>): string[][] {
  const cycles: string[][] = [];
  const done = new Set<string>();
  for (const start of tree.nodes.keys()) {
    if (done.has(start)) continue;
    const path: string[] = [];
    const inPath = new Set<string>();
    let cur: string | null = start;
    while (cur !== null && !done.has(cur) && !inPath.has(cur)) {
      path.push(cur);
      inPath.add(cur);
      cur = parentOf.get(cur) ?? null;
    }
    if (cur !== null && inPath.has(cur)) {
      cycles.push(path.slice(path.indexOf(cur)));
    }
    for (const id of path) done.add(id);
  }
  return cycles;
}

function applyParents(before: Tree, parentOf: Map<string, string | null>): Tree {
  const specs = [...before.nodes.values()].map((n) => ({
    id: n.id,
    parent: parentOf.get(n.id) ?? null,
    fixed: n.fixed,
    grants: n.grants,
    refs: n.refs,
    targetParent: n.targetParent,
  }));
  const problems: string[] = [];
  const tree = buildTree(specs, problems);
  if (!tree) throw new Error('internal: invalid after-tree: ' + problems.join('; '));
  return tree;
}

function isValidAssignment(tree: Tree, parentOf: Map<string, string | null>): boolean {
  return findCycles(tree, parentOf).length === 0;
}

/**
 * 权限例外：迁移后自上而下修正，使每个节点的有效权限与迁移前一致。
 * 例外（grant/deny 条目）放在尽可能高的节点上并随子树继承，数量由最终树唯一确定。
 */
function computeExceptions(before: Tree, after: Tree): ExceptionEntry[] {
  const beforeEff = effectiveGrants(before);
  const entries: ExceptionEntry[] = [];
  const walk = (id: string, inheritedCorrected: Set<string>): void => {
    const node = after.nodes.get(id)!;
    const base = new Set(inheritedCorrected);
    for (const grant of node.grants) base.add(grant);
    const want = beforeEff.get(id)!;
    for (const perm of [...want].sort()) {
      if (!base.has(perm)) entries.push({ node: id, permission: perm, kind: 'grant' });
    }
    for (const perm of [...base].sort()) {
      if (!want.has(perm)) entries.push({ node: id, permission: perm, kind: 'deny' });
    }
    for (const child of node.children) walk(child, want);
  };
  walk(after.rootId, new Set());
  return entries;
}

function computeRefResolutions(before: Tree, after: Tree): RefResolution[] {
  const result: RefResolution[] = [];
  for (const node of before.nodes.values()) {
    for (const ref of node.refs) {
      const beforeId = resolveRef(before, node.id, ref);
      const afterId = resolveRef(after, node.id, ref);
      result.push({
        owner: node.id,
        ref,
        beforeId,
        beforePath: beforeId ? absolutePath(before, beforeId) : null,
        afterId,
        afterPath: afterId ? absolutePath(after, afterId) : null,
        status: classifyRef(beforeId, afterId),
      });
    }
  }
  return result;
}

function classifyRef(beforeId: string | null, afterId: string | null): RefStatus {
  if (beforeId !== null && afterId !== null) {
    return beforeId === afterId ? 'unchanged' : 'redirected';
  }
  if (beforeId !== null) return 'broken';
  if (afterId !== null) return 'fixed';
  return 'broken-both';
}

function evaluateCandidate(
  before: Tree,
  forced: Map<string, string | null>,
  options: BreakerOption[],
): Candidate | null {
  const parentOf = new Map(forced);
  for (const opt of options) parentOf.set(opt.breaker, opt.destination);
  if (!isValidAssignment(before, parentOf)) return null;
  const after = applyParents(before, parentOf);
  const exceptions = computeExceptions(before, after);
  let brokenRefCount = 0;
  for (const node of before.nodes.values()) {
    for (const ref of node.refs) {
      if (resolveRef(before, node.id, ref) !== null && resolveRef(after, node.id, ref) === null) {
        brokenRefCount += 1;
      }
    }
  }
  return { options: options.slice(), parentOf, exceptionCount: exceptions.length, brokenRefCount };
}

function isBetter(candidate: Candidate, best: Candidate): boolean {
  if (candidate.exceptionCount !== best.exceptionCount) {
    return candidate.exceptionCount < best.exceptionCount;
  }
  if (candidate.brokenRefCount !== best.brokenRefCount) {
    return candidate.brokenRefCount < best.brokenRefCount;
  }
  const key = (c: Candidate): string =>
    c.options.map((o) => `${o.breaker}->${o.destination}`).join(',');
  return key(candidate) < key(best);
}

/**
 * 在最小移动数（强制移动 + 每环一次破环移动）的解空间中，
 * 枚举每个环的（破环节点, 目的地）组合，最小化权限例外数。
 */
function searchBestCandidate(
  before: Tree,
  forced: Map<string, string | null>,
  perCycleOptions: BreakerOption[][],
): Candidate | null {
  if (perCycleOptions.length === 0) return evaluateCandidate(before, forced, []);
  let total = 1;
  for (const options of perCycleOptions) total *= options.length;
  if (total <= ENUMERATION_CAP) {
    let best: Candidate | null = null;
    const chosen: BreakerOption[] = new Array(perCycleOptions.length);
    const visit = (cycleIndex: number): void => {
      if (cycleIndex === perCycleOptions.length) {
        const candidate = evaluateCandidate(before, forced, chosen);
        if (candidate && (!best || isBetter(candidate, best))) best = candidate;
        return;
      }
      for (const option of perCycleOptions[cycleIndex]) {
        chosen[cycleIndex] = option;
        visit(cycleIndex + 1);
      }
    };
    visit(0);
    return best;
  }
  // 退化路径：坐标下降（从每环首个选项出发，逐环优化，两轮收敛）。
  const chosen = perCycleOptions.map((options) => options[0]);
  let best = evaluateCandidate(before, forced, chosen);
  if (!best) return null;
  for (let round = 0; round < 2; round += 1) {
    for (let cycleIndex = 0; cycleIndex < perCycleOptions.length; cycleIndex += 1) {
      for (const option of perCycleOptions[cycleIndex]) {
        chosen[cycleIndex] = option;
        const candidate = evaluateCandidate(before, forced, chosen);
        if (candidate && isBetter(candidate, best)) best = candidate;
      }
      chosen[cycleIndex] = best.options[cycleIndex];
    }
  }
  return best;
}
