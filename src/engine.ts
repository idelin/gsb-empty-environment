import type { Decision, NodeDef, NodeState, NodeStatus, OpResult } from './types.js';
import { ApprovalPlan } from './plan.js';

function ok(): OpResult {
  return { ok: true, error: '' };
}

function fail(error: string): OpResult {
  return { ok: false, error };
}

// 审批运行时：驱动节点状态机，处理或签、会签、加签与分支取消。
export class ApprovalEngine {
  private plan: ApprovalPlan;
  private states: Map<string, NodeState>;
  private tick: number;

  constructor(plan: ApprovalPlan) {
    this.plan = plan;
    this.states = new Map();
    this.tick = 0;
    for (const id of plan.ids()) {
      this.attachNode(plan.get(id));
    }
    this.refresh();
  }

  static fromDefs(defs: NodeDef[]): ApprovalEngine {
    const errors = ApprovalPlan.validate(defs);
    if (errors.length > 0) {
      throw new Error(`审批计划非法：${errors.join('；')}`);
    }
    return new ApprovalEngine(new ApprovalPlan(defs));
  }

  getPlan(): ApprovalPlan {
    return this.plan;
  }

  has(id: string): boolean {
    return this.states.has(id);
  }

  getState(id: string): NodeState {
    const st = this.states.get(id);
    if (!st) {
      throw new Error(`节点 ${id} 不存在`);
    }
    return st;
  }

  attachNode(def: NodeDef): void {
    if (this.states.has(def.id)) {
      return;
    }
    this.states.set(def.id, { def, status: 'pending', signatures: [], cancelledAssignees: [], completedTick: -1, note: '' });
    this.refresh();
  }

  detachNode(id: string): void {
    this.states.delete(id);
  }

  // 将所有依赖均已通过的 pending 节点提升为 ready。
  refresh(): void {
    for (const st of this.states.values()) {
      if (st.status !== 'pending') {
        continue;
      }
      let satisfied = true;
      for (const dep of st.def.deps) {
        const depState = this.states.get(dep);
        if (!depState || depState.status !== 'approved') {
          satisfied = false;
          break;
        }
      }
      if (satisfied) {
        st.status = 'ready';
      }
    }
  }

  // 当前可执行（已就绪、等待签署）的节点。
  getExecutable(): NodeState[] {
    const result: NodeState[] = [];
    for (const st of this.states.values()) {
      if (st.status === 'ready') {
        result.push(st);
      }
    }
    return result;
  }

  // 下一批可执行节点的 id 列表。
  nextBatch(): string[] {
    return this.getExecutable().map(st => st.def.id);
  }

  private complete(st: NodeState, status: NodeStatus, note: string): void {
    st.status = status;
    st.note = note;
    st.completedTick = ++this.tick;
    this.refresh();
  }

  sign(id: string, user: string, decision: Decision): OpResult {
    if (decision !== 'approve' && decision !== 'reject') {
      return fail(`非法的签署决定：${decision}`);
    }
    const st = this.states.get(id);
    if (!st) {
      return fail(`节点 ${id} 不存在`);
    }
    if (st.status === 'approved' || st.status === 'rejected') {
      return fail(`节点 ${id} 已完成（${st.status}），不可再签署`);
    }
    if (st.status === 'cancelled') {
      return fail(`节点 ${id} 已取消，不可签署`);
    }
    if (st.status === 'pending') {
      return fail(`节点 ${id} 的前置依赖未满足，尚未就绪`);
    }
    if (st.def.assignees.indexOf(user) < 0) {
      return fail(`用户 ${user} 不是节点 ${id} 的审批人`);
    }
    if (st.cancelledAssignees.indexOf(user) >= 0) {
      return fail(`节点 ${id} 或签已通过，用户 ${user} 的审批任务已取消`);
    }
    if (st.signatures.some(s => s.user === user)) {
      return fail(`用户 ${user} 已在节点 ${id} 签署过，不可重复签署`);
    }
    st.signatures.push({ user, decision, tick: ++this.tick });
    if (decision === 'reject') {
      if (st.def.mode === 'all') {
        this.complete(st, 'rejected', `会签节点被 ${user} 否决`);
      } else {
        const allRejected = st.def.assignees.every(u => st.signatures.some(s => s.user === u && s.decision === 'reject'));
        if (allRejected) {
          this.complete(st, 'rejected', '或签节点所有审批人均拒绝');
        }
      }
      return ok();
    }
    if (st.def.mode === 'any') {
      for (const u of st.def.assignees) {
        if (u !== user && !st.signatures.some(s => s.user === u)) {
          st.cancelledAssignees.push(u);
        }
      }
      this.complete(st, 'approved', `或签节点由 ${user} 通过`);
      return ok();
    }
    const allApproved = st.def.assignees.every(u => st.signatures.some(s => s.user === u && s.decision === 'approve'));
    if (allApproved) {
      this.complete(st, 'approved', '会签节点全员通过');
    }
    return ok();
  }

  // 运行中加签：向未结束的节点追加审批人。
  addAssignees(id: string, users: string[]): OpResult {
    const st = this.states.get(id);
    if (!st) {
      return fail(`节点 ${id} 不存在`);
    }
    if (st.status === 'approved' || st.status === 'rejected' || st.status === 'cancelled') {
      return fail(`节点 ${id} 已结束（${st.status}），无法加签`);
    }
    const added: string[] = [];
    for (const u of users) {
      if (st.def.assignees.indexOf(u) < 0 && added.indexOf(u) < 0) {
        st.def.assignees.push(u);
        added.push(u);
      }
    }
    if (added.length === 0) {
      return fail(`加签用户均已是节点 ${id} 的审批人`);
    }
    return ok();
  }

  // 取消未开始的分支节点：仅允许没有任何签署记录的节点。
  cancelNode(id: string, reason: string): OpResult {
    const st = this.states.get(id);
    if (!st) {
      return fail(`节点 ${id} 不存在`);
    }
    if (st.status === 'approved' || st.status === 'rejected') {
      return fail(`节点 ${id} 已完成（${st.status}），不可取消`);
    }
    if (st.status === 'cancelled') {
      return fail(`节点 ${id} 已是取消状态`);
    }
    if (st.signatures.length > 0) {
      return fail(`节点 ${id} 已有签署记录，不可取消`);
    }
    st.status = 'cancelled';
    st.note = reason;
    st.completedTick = ++this.tick;
    return ok();
  }
}
