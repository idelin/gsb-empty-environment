import type {
  Move,
  PermissionException,
  PlanResult,
  RefResolution,
  RefStatus,
  TargetGrouping,
  TreeState,
  Violation,
} from "./types.ts";
import {
  applyMove,
  descendantsOf,
  effectiveAcl,
  isAncestor,
  pathFromRoot,
  resolveRef,
  stateKey,
} from "./tree.ts";

export interface PlanOptions {
  maxMoves?: number;
  stateBudget?: number;
}

export function findViolations(state: TreeState, target: TargetGrouping): Violation[] {
  const out: Violation[] = [];
  for (const [node, container] of Object.entries(target.mustBeUnder)) {
    if (!isAncestor(state, container, node)) out.push({ node, container });
  }
  return out;
}

export function permissionExceptions(before: TreeState, after: TreeState): PermissionException[] {
  const out: PermissionException[] = [];
  for (const id of before.parent.keys()) {
    const b = effectiveAcl(before, id);
    const a = effectiveAcl(after, id);
    const gained = [...a].filter((g) => !b.has(g)).sort();
    const lost = [...b].filter((g) => !a.has(g)).sort();
    if (gained.length > 0 || lost.length > 0) out.push({ nodeId: id, gained, lost });
  }
  return out;
}

function exceptionScore(exceptions: PermissionException[]): [number, number] {
  const entries = exceptions.reduce((sum, e) => sum + e.gained.length + e.lost.length, 0);
  return [exceptions.length, entries];
}

export function collectRefResolutions(before: TreeState, after: TreeState): RefResolution[] {
  const out: RefResolution[] = [];
  for (const [nodeId, refs] of before.refs) {
    for (const path of refs) {
      const b = resolveRef(before, nodeId, path);
      const a = resolveRef(after, nodeId, path);
      let status: RefStatus;
      if (b !== null && a !== null) status = b === a ? "UNCHANGED" : "RETARGETED";
      else if (b !== null) status = "BROKEN";
      else if (a !== null) status = "REPAIRED";
      else status = "STILL_BROKEN";
      out.push({ nodeId, path, before: b, after: a, status });
    }
  }
  return out;
}

function validateTarget(state: TreeState, target: TargetGrouping): void {
  for (const [node, container] of Object.entries(target.mustBeUnder)) {
    if (!state.parent.has(node)) throw new Error(`目标分组引用了不存在的节点: ${node}`);
    if (!state.parent.has(container)) throw new Error(`目标分组引用了不存在的容器: ${container}`);
    if (node === container) throw new Error(`节点 ${node} 不能要求位于自身之下`);
  }
}

function candidateMoves(state: TreeState, violation: Violation): Move[] {
  const { node, container } = violation;
  const anchors = [container, ...descendantsOf(state, container)];
  const path = pathFromRoot(state, node).reverse();
  const moves: Move[] = [];
  const seen = new Set<string>();
  for (const a of path) {
    if (a === state.root || state.pinned.has(a)) continue;
    for (const p of anchors) {
      if (p === a || isAncestor(state, a, p)) continue;
      if (state.parent.get(a) === p) continue;
      const key = `${a}->${p}`;
      if (seen.has(key)) continue;
      seen.add(key);
      moves.push({ nodeId: a, newParentId: p });
    }
  }
  return moves;
}

export function planMigration(
  initial: TreeState,
  target: TargetGrouping,
  options: PlanOptions = {},
): PlanResult | null {
  validateTarget(initial, target);
  const maxMoves = options.maxMoves ?? initial.parent.size;
  const stateBudget = options.stateBudget ?? 200_000;
  let explored = 0;

  for (let depth = 0; depth <= maxMoves; depth++) {
    const seenAtDepth = new Map<string, number>();
    const solutions: Array<{ moves: Move[]; state: TreeState; score: [number, number] }> = [];

    const visit = (state: TreeState, movesLeft: number, movesSoFar: Move[]): void => {
      if (++explored > stateBudget) {
        throw new Error(`搜索超出状态预算 ${stateBudget}，请缩小问题规模或提高预算`);
      }
      const violations = findViolations(state, target);
      if (violations.length === 0) {
        solutions.push({
          moves: [...movesSoFar],
          state,
          score: exceptionScore(permissionExceptions(initial, state)),
        });
        return;
      }
      if (movesLeft === 0) return;
      const key = stateKey(state);
      const prev = seenAtDepth.get(key);
      if (prev !== undefined && prev >= movesLeft) return;
      seenAtDepth.set(key, movesLeft);
      for (const move of candidateMoves(state, violations[0])) {
        visit(applyMove(state, move), movesLeft - 1, [...movesSoFar, move]);
      }
    };

    visit(initial, depth, []);

    if (solutions.length > 0) {
      solutions.sort((x, y) => x.score[0] - y.score[0] || x.score[1] - y.score[1]);
      const best = solutions[0]!;
      const exceptions = permissionExceptions(initial, best.state);
      return {
        moves: best.moves,
        finalState: best.state,
        exceptions,
        exceptionNodeCount: exceptions.length,
        exceptionEntryCount: exceptions.reduce((s, e) => s + e.gained.length + e.lost.length, 0),
        refResolutions: collectRefResolutions(initial, best.state),
        exploredStates: explored,
      };
    }
  }
  return null;
}

export function assertIdentityPreserved(before: TreeState, after: TreeState): void {
  const beforeIds = [...before.parent.keys()].sort();
  const afterIds = [...after.parent.keys()].sort();
  if (beforeIds.length !== afterIds.length || beforeIds.some((id, i) => id !== afterIds[i])) {
    throw new Error("迁移后节点集合发生变化：禁止通过复制节点虚增身份");
  }
  for (const id of beforeIds) {
    if (before.name.get(id) !== after.name.get(id)) throw new Error(`节点 ${id} 的名称被篡改`);
    if (before.localAcl.get(id) !== after.localAcl.get(id)) {
      throw new Error(`节点 ${id} 的本地权限被篡改`);
    }
  }
  for (const id of before.pinned) {
    if (before.parent.get(id) !== after.parent.get(id)) {
      throw new Error(`固定节点 ${id} 被移动`);
    }
  }
}
