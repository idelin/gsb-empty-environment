import type { AbstractState } from './state.ts';
import type { CfgEdge, CfgNode, Operation, PathStep, Violation, ViolationKind } from './types.ts';

export interface ReportContext {
  report: (kind: ViolationKind, location: string, extra?: { variable?: string; handle?: string; deferred?: boolean }) => void;
  locationOf: (node: CfgNode) => string;
}

function hasStatus(s: AbstractState, variable: string, status: 'owned' | 'moved' | 'closed'): boolean {
  return s.status.get(variable)?.has(status) ?? false;
}

/** 变量当前是否仍可使用（已获取且未被移动/关闭）。 */
function isUsable(s: AbstractState, variable: string): boolean {
  return hasStatus(s, variable, 'owned');
}

function activeHandles(s: AbstractState, variable: string): string[] {
  const out: string[] = [];
  for (const [handle, vars] of s.borrows) {
    if (vars.has(variable)) out.push(handle);
  }
  return out;
}

/**
 * 关闭（立即或延迟）一个变量：
 * - 已关闭 -> doubleClose；已移动 -> useAfterMove；未获取 -> useUninitialized
 * - 仍有活跃借用 -> borrowAcrossClose（借用不得越过关闭）
 */
function applyClose(s: AbstractState, variable: string, location: string, deferred: boolean, ctx: ReportContext): void {
  if (hasStatus(s, variable, 'closed')) {
    ctx.report('doubleClose', location, { variable, deferred });
    return;
  }
  if (hasStatus(s, variable, 'moved') && !hasStatus(s, variable, 'owned')) {
    ctx.report('useAfterMove', location, { variable, deferred });
    return;
  }
  if (!s.status.has(variable)) {
    ctx.report('useUninitialized', location, { variable, deferred });
    return;
  }
  for (const handle of activeHandles(s, variable)) {
    ctx.report('borrowAcrossClose', location, { variable, handle, deferred });
  }
  s.borrows.forEach((vars, handle) => {
    if (vars.has(variable)) vars.delete(variable);
    if (vars.size === 0) s.borrows.delete(handle);
  });
  s.status.set(variable, new Set(['closed']));
}

/** 应用节点自身操作（仅在正常边上执行）。 */
export function applyNode(s: AbstractState, node: CfgNode, ctx: ReportContext): void {
  const op: Operation = node.operation;
  const loc = ctx.locationOf(node);
  switch (op.op) {
    case 'entry':
    case 'noop':
    case 'catch':
    case 'throw':
      return;

    case 'acquire': {
      // 重新获取会重建所有权（覆盖关闭/移动历史），并清掉残留借用。
      s.borrows.forEach((vars, handle) => {
        if (vars.has(op.variable)) vars.delete(op.variable);
        if (vars.size === 0) s.borrows.delete(handle);
      });
      s.status.set(op.variable, new Set(['owned']));
      return;
    }

    case 'move': {
      if (hasStatus(s, op.from, 'closed')) {
        ctx.report('useAfterClose', loc, { variable: op.from });
        return;
      }
      if (hasStatus(s, op.from, 'moved') && !hasStatus(s, op.from, 'owned')) {
        ctx.report('useAfterMove', loc, { variable: op.from });
        return;
      }
      if (!s.status.has(op.from)) {
        ctx.report('useUninitialized', loc, { variable: op.from });
        return;
      }
      if (activeHandles(s, op.from).length > 0) {
        ctx.report('moveWhileBorrowed', loc, { variable: op.from, handle: activeHandles(s, op.from)[0] });
      }
      // 借用随所有权一并转移到目标变量，原变量失效。
      s.borrows.forEach((vars) => {
        if (vars.has(op.from)) {
          vars.delete(op.from);
          vars.add(op.to);
        }
      });
      s.status.set(op.from, new Set(['moved']));
      s.status.set(op.to, new Set(['owned']));
      return;
    }

    case 'borrow': {
      if (hasStatus(s, op.variable, 'closed')) {
        ctx.report('useAfterClose', loc, { variable: op.variable, handle: op.handle });
        return;
      }
      if (hasStatus(s, op.variable, 'moved') && !hasStatus(s, op.variable, 'owned')) {
        ctx.report('useAfterMove', loc, { variable: op.variable, handle: op.handle });
        return;
      }
      if (!s.status.has(op.variable)) {
        ctx.report('useUninitialized', loc, { variable: op.variable, handle: op.handle });
        return;
      }
      const existing = s.borrows.get(op.handle);
      if (existing) existing.add(op.variable);
      else s.borrows.set(op.handle, new Set([op.variable]));
      return;
    }

    case 'restore': {
      const vars = s.borrows.get(op.handle);
      if (!vars || vars.size === 0) {
        ctx.report('invalidRestore', loc, { handle: op.handle });
        return;
      }
      s.borrows.delete(op.handle);
      return;
    }

    case 'close': {
      applyClose(s, op.variable, loc, false, ctx);
      return;
    }

    case 'deferClose': {
      if (hasStatus(s, op.variable, 'closed')) {
        ctx.report('doubleClose', loc, { variable: op.variable, deferred: true });
        return;
      }
      if (hasStatus(s, op.variable, 'moved') && !hasStatus(s, op.variable, 'owned')) {
        ctx.report('useAfterMove', loc, { variable: op.variable, deferred: true });
        return;
      }
      if (!s.status.has(op.variable)) {
        ctx.report('useUninitialized', loc, { variable: op.variable, deferred: true });
        return;
      }
      const list = s.pending.get(op.region);
      if (list) list.push(op.variable);
      else s.pending.set(op.region, [op.variable]);
      return;
    }

    case 'enterRegion': {
      s.regionStack.push(op.region);
      return;
    }

    case 'exitRegion': {
      const idx = s.regionStack.lastIndexOf(op.region);
      if (idx >= 0) {
        const closedRegions = s.regionStack.splice(idx).reverse();
        for (const region of closedRegions) {
          const list = s.pending.get(region);
          if (list) {
            for (const variable of [...list].reverse()) {
              applyClose(s, variable, `${loc}#defer:${region}:${variable}`, true, ctx);
            }
            s.pending.delete(region);
          }
        }
      }
      return;
    }
  }
}

/**
 * 沿边转移：异常边先执行显式 cleanup（不应用节点操作），正常边无附加效果。
 * cleanup 位置形如 edge:A->H#i，便于在见证路径上定位。
 */
export function applyEdge(s: AbstractState, edge: CfgEdge, ctx: ReportContext): void {
  if (edge.kind !== 'exception' || !edge.cleanup) return;
  edge.cleanup.forEach((variable, i) => {
    applyClose(s, variable, `edge:${edge.from}->${edge.to}#${i}`, true, ctx);
  });
  // 展开覆盖的 region 已离开，注销其延迟清理记录，避免重入时重复关闭。
  for (const region of edge.cleanedRegions ?? []) {
    s.pending.delete(region);
    const idx = s.regionStack.lastIndexOf(region);
    if (idx >= 0) s.regionStack.splice(idx);
  }
}

export function makeViolation(
  kind: ViolationKind,
  location: string,
  extra?: { variable?: string; handle?: string; deferred?: boolean },
): Violation {
  return {
    kind,
    location,
    variable: extra?.variable,
    handle: extra?.handle,
    deferred: extra?.deferred ?? false,
  };
}

export function violationKey(v: Violation): string {
  return [v.kind, v.location, v.variable ?? '', v.handle ?? '', v.deferred ? 'd' : ''].join('|');
}

export function edgeStep(edge: CfgEdge): PathStep {
  return {
    kind: 'edge',
    from: edge.from,
    to: edge.to,
    edgeKind: edge.kind,
    cleanup: edge.cleanup ? [...edge.cleanup] : undefined,
  };
}

export function nodeStep(nodeId: string): PathStep {
  return { kind: 'node', node: nodeId };
}
