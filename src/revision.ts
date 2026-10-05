import type { NodeDef } from './types.js';
import type { ApprovalPlan } from './plan.js';
import { ApprovalEngine } from './engine.js';

export type RevisionOp = { kind: 'addNode'; node: NodeDef } | { kind: 'removeNode'; id: string } | { kind: 'addDependency'; from: string; to: string } | { kind: 'addAssignees'; id: string; users: string[] } | { kind: 'withdraw'; id: string };

export interface RevisionError {
  op: string;
  reason: string;
}

export interface RevisionResult {
  ok: boolean;
  errors: RevisionError[];
}

function describe(op: RevisionOp): string {
  if (op.kind === 'addNode') {
    return `新增节点 ${op.node.id}`;
  }
  if (op.kind === 'removeNode') {
    return `移除节点 ${op.id}`;
  }
  if (op.kind === 'addDependency') {
    return `新增依赖 ${op.from} -> ${op.to}`;
  }
  if (op.kind === 'addAssignees') {
    return `节点 ${op.id} 加签（${op.users.join('、')}）`;
  }
  return `撤回节点 ${op.id}`;
}

// 在计划草稿上校验单个修订操作；通过时把变更应用到草稿，返回空串表示合法。
function validateOp(engine: ApprovalEngine, draft: ApprovalPlan, op: RevisionOp): string {
  if (op.kind === 'addNode') {
    const def = op.node;
    if (draft.has(def.id)) {
      return `节点 ${def.id} 已存在，无法重复新增`;
    }
    if (def.assignees.length === 0) {
      return `节点 ${def.id} 缺少审批人`;
    }
    for (const dep of def.deps) {
      if (!draft.has(dep)) {
        return `节点 ${def.id} 依赖了不存在的节点 ${dep}`;
      }
    }
    draft.addNode({ id: def.id, label: def.label, mode: def.mode, assignees: def.assignees.slice(), deps: def.deps.slice() });
    const cycle = draft.findCycle();
    if (cycle.length > 0) {
      draft.removeNode(def.id);
      return `新增节点 ${def.id} 后依赖出现环路，涉及节点：${cycle.join('、')}`;
    }
    return '';
  }
  if (op.kind === 'removeNode') {
    if (!draft.has(op.id)) {
      return `节点 ${op.id} 不存在`;
    }
    if (engine.has(op.id)) {
      const st = engine.getState(op.id);
      if (st.status === 'approved' || st.status === 'rejected') {
        return `节点 ${op.id} 已完成，不可移除`;
      }
      if (st.signatures.length > 0) {
        return `节点 ${op.id} 已有签署记录，不可移除`;
      }
    }
    const dependents = draft.dependentsOf(op.id);
    if (dependents.length > 0) {
      return `节点 ${op.id} 仍被 ${dependents.join('、')} 依赖，不可移除`;
    }
    draft.removeNode(op.id);
    return '';
  }
  if (op.kind === 'addDependency') {
    const from = op.from;
    const to = op.to;
    if (from === to) {
      return `节点 ${from} 不能依赖自身`;
    }
    if (!draft.has(from)) {
      return `前置节点 ${from} 不存在`;
    }
    if (!draft.has(to)) {
      return `节点 ${to} 不存在`;
    }
    if (draft.depsOf(to).indexOf(from) >= 0) {
      return `依赖 ${from} -> ${to} 已存在，无需重复添加`;
    }
    const path = draft.findPath(to, from);
    if (path.length > 0) {
      return `新增依赖 ${from} -> ${to} 会形成环路：${path.join(' -> ')} -> ${to}，拒绝`;
    }
    if (engine.has(from)) {
      const fromState = engine.getState(from);
      if (fromState.status === 'cancelled') {
        return `前置节点 ${from} 已取消，无法作为新的依赖`;
      }
    }
    if (engine.has(to)) {
      const toState = engine.getState(to);
      if (toState.status === 'approved' || toState.status === 'rejected') {
        return `节点 ${to} 已按既有顺序完成，新增前置依赖 ${from} 将改变既有执行顺序，拒绝`;
      }
      if (toState.signatures.length > 0) {
        return `节点 ${to} 已开始签署，新增前置依赖 ${from} 将改变既有执行顺序，拒绝`;
      }
    }
    draft.addDependency(from, to);
    return '';
  }
  if (op.kind === 'addAssignees') {
    if (!draft.has(op.id)) {
      return `节点 ${op.id} 不存在`;
    }
    if (engine.has(op.id)) {
      const st = engine.getState(op.id);
      if (st.status === 'approved' || st.status === 'rejected' || st.status === 'cancelled') {
        return `节点 ${op.id} 已结束（${st.status}），无法加签`;
      }
      const fresh = op.users.filter(u => st.def.assignees.indexOf(u) < 0);
      if (fresh.length === 0) {
        return `加签用户均已是节点 ${op.id} 的审批人`;
      }
    }
    return '';
  }
  if (!draft.has(op.id)) {
    return `节点 ${op.id} 不存在`;
  }
  if (engine.has(op.id)) {
    const st = engine.getState(op.id);
    if (st.status === 'approved' || st.status === 'rejected') {
      return `节点 ${op.id} 已完成（${st.status}），不可撤回`;
    }
    if (st.signatures.length > 0) {
      return `节点 ${op.id} 已有签署记录，不可撤回`;
    }
  }
  return '';
}

function applyOp(engine: ApprovalEngine, op: RevisionOp): void {
  const plan = engine.getPlan();
  if (op.kind === 'addNode') {
    plan.addNode(op.node);
    engine.attachNode(op.node);
    return;
  }
  if (op.kind === 'removeNode') {
    plan.removeNode(op.id);
    engine.detachNode(op.id);
    return;
  }
  if (op.kind === 'addDependency') {
    plan.addDependency(op.from, op.to);
    return;
  }
  if (op.kind === 'addAssignees') {
    engine.addAssignees(op.id, op.users);
    return;
  }
  engine.cancelNode(op.id, '计划修订撤回');
}

// 计划修订：先整体校验（原子性），全部合法才应用；任一操作非法则拒绝并给出解释。
export function revise(engine: ApprovalEngine, ops: RevisionOp[]): RevisionResult {
  const draft = engine.getPlan().clone();
  const errors: RevisionError[] = [];
  for (const op of ops) {
    const reason = validateOp(engine, draft, op);
    if (reason) {
      errors.push({ op: describe(op), reason });
    }
  }
  if (errors.length > 0) {
    return { ok: false, errors };
  }
  for (const op of ops) {
    applyOp(engine, op);
  }
  engine.refresh();
  return { ok: true, errors: [] };
}
