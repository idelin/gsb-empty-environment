/**
 * 审批计划库 —— 核心实现。
 *
 * 能力：
 * - 前后依赖（DAG）调度，输出下一批可执行节点；
 * - 或签(any) / 会签(all)；
 * - 或签组：任一分支通过后，未开始分支自动取消，组外依赖不受影响；
 * - 运行中加签：insertAfter 插入后续审批节点 / addApprover 追加审批人；
 * - 计划修订：已完成节点不可撤回；新增依赖若要求改变既有执行顺序则拒绝并解释。
 */

import type {
  Decision,
  NodeSnapshot,
  NodeSpec,
  NodeStatus,
  PlanRevision,
  PlanSpec,
  RevisionReport,
} from './types.ts';

export class PlanError extends Error {}
export class RevisionRejected extends PlanError {}

interface NodeRecord {
  spec: NodeSpec;
  status: NodeStatus;
  decisions: Decision[];
  startedSeq: number | null;
  completedSeq: number | null;
  cancelReason?: string;
}

const TERMINAL: ReadonlySet<NodeStatus> = new Set(['approved', 'rejected', 'cancelled']);

export class ApprovalPlan {
  private nodes = new Map<string, NodeRecord>();
  private clock = 0;

  constructor(spec: PlanSpec) {
    for (const node of spec.nodes) this.addNodeChecked(node);
    this.validateReferences();
    this.assertAcyclic([...this.nodes.keys()]);
  }

  // ---------------------------------------------------------------- 查询

  /** 下一批可执行节点：状态为 pending 且全部前置依赖已满足。 */
  getNextExecutable(): NodeSnapshot[] {
    const ready: NodeSnapshot[] = [];
    for (const record of this.nodes.values()) {
      if (record.status === 'pending' && this.depsSatisfied(record.spec)) {
        ready.push(this.snapshotOf(record));
      }
    }
    return ready;
  }

  getNode(id: string): NodeSnapshot {
    return this.snapshotOf(this.mustGet(id));
  }

  /** 或签组是否已通过（任一分支 approved）。 */
  isGroupApproved(groupId: string): boolean {
    for (const record of this.nodes.values()) {
      if (record.spec.group === groupId && record.status === 'approved') return true;
    }
    return false;
  }

  /** 计划是否整体终态（无 pending / in_progress 节点）。 */
  isSettled(): boolean {
    for (const record of this.nodes.values()) {
      if (!TERMINAL.has(record.status)) return false;
    }
    return true;
  }

  // ---------------------------------------------------------------- 执行

  /** 提交一次审批决定。pending 且依赖已满足的节点会在首次决定时自动启动。 */
  decide(nodeId: string, approverId: string, decision: 'approved' | 'rejected'): NodeSnapshot {
    const record = this.mustGet(nodeId);
    if (TERMINAL.has(record.status)) {
      throw new PlanError(`节点 ${nodeId} 已处于终态(${record.status})，不可再审批`);
    }
    if (!record.spec.approvers.includes(approverId)) {
      throw new PlanError(`${approverId} 不是节点 ${nodeId} 的审批人`);
    }
    if (record.decisions.some((d) => d.approverId === approverId)) {
      throw new PlanError(`${approverId} 已对节点 ${nodeId} 作出过决定`);
    }
    if (record.status === 'pending') {
      if (!this.depsSatisfied(record.spec)) {
        throw new PlanError(`节点 ${nodeId} 的前置依赖尚未满足，不可启动`);
      }
      record.status = 'in_progress';
      record.startedSeq = ++this.clock;
    }
    record.decisions.push({ approverId, decision, seq: ++this.clock });

    if (decision === 'rejected') {
      // 任一审批人拒绝即节点拒绝（或签与会签一致）
      record.status = 'rejected';
      record.completedSeq = ++this.clock;
      return this.snapshotOf(record);
    }
    const approvedCount = record.decisions.filter((d) => d.decision === 'approved').length;
    const need = record.spec.mode === 'any' ? 1 : record.spec.approvers.length;
    if (approvedCount >= need) this.completeApproved(record);
    return this.snapshotOf(record);
  }

  approve(nodeId: string, approverId: string): NodeSnapshot {
    return this.decide(nodeId, approverId, 'approved');
  }

  reject(nodeId: string, approverId: string): NodeSnapshot {
    return this.decide(nodeId, approverId, 'rejected');
  }

  /** 撤回节点。已完成（approved/rejected）节点不可撤回。 */
  withdraw(nodeId: string): NodeSnapshot {
    const record = this.mustGet(nodeId);
    if (record.status === 'approved' || record.status === 'rejected') {
      throw new RevisionRejected(`节点 ${nodeId} 已完成(${record.status})，不可撤回`);
    }
    if (record.status === 'cancelled') {
      throw new RevisionRejected(`节点 ${nodeId} 已被取消，不可撤回`);
    }
    record.status = 'cancelled';
    record.completedSeq = ++this.clock;
    record.cancelReason = '提交方撤回';
    return this.snapshotOf(record);
  }

  // ---------------------------------------------------------------- 加签

  /**
   * 运行中插入加签节点：新节点挂在 anchor 之后，
   * 原本依赖 anchor 的未开始节点改为依赖新节点。
   * 若任一依赖 anchor 的节点已开始/完成，则重挂会改变既有顺序，拒绝并解释。
   */
  insertAfter(anchorId: string, spec: NodeSpec): NodeSnapshot {
    const anchor = this.mustGet(anchorId);
    if (anchor.status === 'cancelled' || anchor.status === 'rejected') {
      throw new RevisionRejected(`无法在已${anchor.status === 'cancelled' ? '取消' : '拒绝'}的节点 ${anchorId} 后加签`);
    }
    if (this.nodes.has(spec.id)) {
      throw new RevisionRejected(`加签节点 id ${spec.id} 已存在`);
    }
    const dependents = [...this.nodes.values()].filter(
      (r) => r.spec.dependsOn?.includes(anchorId),
    );
    for (const dep of dependents) {
      if (dep.status !== 'pending') {
        throw new RevisionRejected(
          `拒绝加签：节点 ${dep.spec.id} 已依赖 ${anchorId} 并于序号 ${dep.startedSeq} 开始执行，` +
            `在其间插入 ${spec.id} 将改变既有执行顺序`,
        );
      }
    }
    const inserted: NodeSpec = { ...spec, dependsOn: [anchorId] };
    this.addNodeChecked(inserted);
    for (const dep of dependents) {
      dep.spec = {
        ...dep.spec,
        dependsOn: dep.spec.dependsOn!.map((d) => (d === anchorId ? spec.id : d)),
      };
    }
    this.assertAcyclic([spec.id]);
    return this.snapshotOf(this.nodes.get(spec.id)!);
  }

  /** 节点内加签：为未完成的节点追加审批人（会签下新人也须通过）。 */
  addApprover(nodeId: string, approverId: string): NodeSnapshot {
    const record = this.mustGet(nodeId);
    if (TERMINAL.has(record.status)) {
      throw new RevisionRejected(`节点 ${nodeId} 已处于终态(${record.status})，不可加签审批人`);
    }
    if (record.spec.approvers.includes(approverId)) {
      throw new RevisionRejected(`${approverId} 已是节点 ${nodeId} 的审批人`);
    }
    record.spec = { ...record.spec, approvers: [...record.spec.approvers, approverId] };
    return this.snapshotOf(record);
  }

  // ---------------------------------------------------------------- 计划修订

  /**
   * 应用计划修订，逐条生效/拒绝，返回报告。
   * 规则：
   * - 已完成（approved/rejected）节点不可撤回/移除；
   * - 新增依赖若成环，或要求"尚未完成的前置先于已开始的节点"，即改变既有顺序，拒绝并解释。
   */
  revise(revision: PlanRevision): RevisionReport {
    const report: RevisionReport = { applied: [], rejected: [] };

    for (const id of revision.removeNodes ?? []) {
      const label = `removeNode(${id})`;
      const record = this.nodes.get(id);
      if (!record) {
        report.rejected.push({ change: label, reason: `节点 ${id} 不存在` });
        continue;
      }
      if (record.status === 'approved' || record.status === 'rejected') {
        report.rejected.push({
          change: label,
          reason: `节点 ${id} 已完成(${record.status})，不可撤回`,
        });
        continue;
      }
      if (record.status !== 'pending') {
        report.rejected.push({
          change: label,
          reason: `节点 ${id} 状态为 ${record.status}，仅 pending 节点可移除`,
        });
        continue;
      }
      const dependents = [...this.nodes.values()].filter(
        (r) => r.spec.id !== id && r.spec.dependsOn?.includes(id),
      );
      if (dependents.length > 0) {
        report.rejected.push({
          change: label,
          reason: `节点 ${id} 仍被 ${dependents.map((r) => r.spec.id).join(', ')} 依赖`,
        });
        continue;
      }
      this.nodes.delete(id);
      report.applied.push(label);
    }

    for (const spec of revision.addNodes ?? []) {
      const label = `addNode(${spec.id})`;
      try {
        this.addNodeChecked(spec);
        this.validateReferences();
        this.assertAcyclic([spec.id]);
        report.applied.push(label);
      } catch (err) {
        this.nodes.delete(spec.id);
        report.rejected.push({ change: label, reason: (err as Error).message });
      }
    }

    for (const { from, to } of revision.addDependencies ?? []) {
      const label = `addDependency(${from} <- ${to})`;
      const reason = this.checkAddDependency(from, to);
      if (reason) {
        report.rejected.push({ change: label, reason });
        continue;
      }
      const record = this.nodes.get(from)!;
      record.spec = { ...record.spec, dependsOn: [...(record.spec.dependsOn ?? []), to] };
      report.applied.push(label);
    }

    for (const { from, to } of revision.removeDependencies ?? []) {
      const label = `removeDependency(${from} <- ${to})`;
      const record = this.nodes.get(from);
      if (!record || !record.spec.dependsOn?.includes(to)) {
        report.rejected.push({ change: label, reason: `依赖 ${from} <- ${to} 不存在` });
        continue;
      }
      record.spec = {
        ...record.spec,
        dependsOn: record.spec.dependsOn!.filter((d) => d !== to),
      };
      report.applied.push(label);
    }

    return report;
  }

  // ---------------------------------------------------------------- 内部

  private mustGet(id: string): NodeRecord {
    const record = this.nodes.get(id);
    if (!record) throw new PlanError(`节点 ${id} 不存在`);
    return record;
  }

  private addNodeChecked(spec: NodeSpec): void {
    if (!spec.id) throw new PlanError('节点缺少 id');
    if (this.nodes.has(spec.id)) throw new PlanError(`节点 id ${spec.id} 重复`);
    if (spec.approvers.length === 0) throw new PlanError(`节点 ${spec.id} 至少需一名审批人`);
    if (spec.dependsOn?.includes(spec.id)) throw new PlanError(`节点 ${spec.id} 不可依赖自身`);
    this.nodes.set(spec.id, {
      spec: { ...spec, dependsOn: [...(spec.dependsOn ?? [])], approvers: [...spec.approvers] },
      status: 'pending',
      decisions: [],
      startedSeq: null,
      completedSeq: null,
    });
  }

  private validateReferences(): void {
    const groupIds = new Set<string>();
    for (const record of this.nodes.values()) {
      if (record.spec.group) groupIds.add(record.spec.group);
    }
    for (const record of this.nodes.values()) {
      for (const dep of record.spec.dependsOn ?? []) {
        if (!this.nodes.has(dep) && !groupIds.has(dep)) {
          throw new PlanError(`节点 ${record.spec.id} 依赖了不存在的节点/或签组 ${dep}`);
        }
      }
    }
  }

  /** 依赖是否满足：dep 为节点时需 approved；为或签组时任一分支通过即满足。 */
  private depSatisfied(dep: string): boolean {
    const record = this.nodes.get(dep);
    if (record) return record.status === 'approved';
    return this.isGroupApproved(dep);
  }

  private depsSatisfied(spec: NodeSpec): boolean {
    return (spec.dependsOn ?? []).every((dep) => this.depSatisfied(dep));
  }

  private completeApproved(record: NodeRecord): void {
    record.status = 'approved';
    record.completedSeq = ++this.clock;
    const groupId = record.spec.group;
    if (!groupId) return;
    // 或签组通过：取消其余未开始分支；进行中的分支不强制取消（由业务决定）。
    // 组外节点依赖的是组本身，分支取消不影响外部依赖的满足判定。
    for (const sibling of this.nodes.values()) {
      if (sibling.spec.group === groupId && sibling.spec.id !== record.spec.id
          && sibling.status === 'pending') {
        sibling.status = 'cancelled';
        sibling.completedSeq = ++this.clock;
        sibling.cancelReason = `或签组 ${groupId} 已由分支 ${record.spec.id} 通过`;
      }
    }
  }

  /** 校验新增依赖 from <- to（to 须先于 from）。返回拒绝原因，null 表示可接受。 */
  private checkAddDependency(from: string, to: string): string | null {
    const fromRecord = this.nodes.get(from);
    if (!fromRecord) return `节点 ${from} 不存在`;
    if (from === to) return `节点 ${from} 不可依赖自身`;
    if (fromRecord.spec.dependsOn?.includes(to)) return `依赖 ${from} <- ${to} 已存在`;
    const toIsNode = this.nodes.has(to);
    const toIsGroup = [...this.nodes.values()].some((r) => r.spec.group === to);
    if (!toIsNode && !toIsGroup) return `前置 ${to} 既不是节点也不是或签组`;

    // 既有顺序约束：from 已开始/完成时，to 必须在 from 启动前已完成，
    // 否则接受该依赖等于要求"已发生的执行"等待"尚未发生的前置"，须拒绝。
    if (fromRecord.status !== 'pending') {
      const satisfiedInTime =
        toIsNode
          ? this.nodes.get(to)!.status === 'approved' &&
            this.nodes.get(to)!.completedSeq! < fromRecord.startedSeq!
          : this.groupApprovedBefore(to, fromRecord.startedSeq!);
      if (!satisfiedInTime) {
        return (
          `拒绝新增依赖 ${from} <- ${to}：节点 ${from} 已于序号 ${fromRecord.startedSeq} 开始执行` +
          `（当前状态 ${fromRecord.status}），而前置 ${to} 在此之前并未完成，` +
          `接受该依赖将要求改变既有执行顺序`
        );
      }
    }

    // 环检测：沿 to 的既有依赖链若能回到 from，则成环。
    if (toIsNode && this.reaches(to, from)) {
      return `拒绝新增依赖 ${from} <- ${to}：将形成依赖环（${to} 的依赖链可回溯至 ${from}）`;
    }
    return null;
  }

  private groupApprovedBefore(groupId: string, seq: number): boolean {
    for (const record of this.nodes.values()) {
      if (record.spec.group === groupId && record.status === 'approved'
          && record.completedSeq! < seq) {
        return true;
      }
    }
    return false;
  }

  /** 从 start 沿 dependsOn 链是否可到达 target。 */
  private reaches(start: string, target: string): boolean {
    const visited = new Set<string>();
    const stack = [start];
    while (stack.length > 0) {
      const cur = stack.pop()!;
      if (cur === target) return true;
      if (visited.has(cur)) continue;
      visited.add(cur);
      const record = this.nodes.get(cur);
      for (const dep of record?.spec.dependsOn ?? []) stack.push(dep);
    }
    return false;
  }

  private assertAcyclic(ids: string[]): void {
    for (const id of ids) {
      const record = this.nodes.get(id);
      for (const dep of record?.spec.dependsOn ?? []) {
        if (this.reaches(dep, id)) {
          throw new PlanError(`依赖图存在环：${id} 经 ${dep} 可回溯至自身`);
        }
      }
    }
  }

  private snapshotOf(record: NodeRecord): NodeSnapshot {
    const decided = new Set(record.decisions.map((d) => d.approverId));
    const settled = TERMINAL.has(record.status);
    return {
      id: record.spec.id,
      name: record.spec.name,
      mode: record.spec.mode,
      status: record.status,
      approvers: [...record.spec.approvers],
      pendingApprovers: settled ? [] : record.spec.approvers.filter((a) => !decided.has(a)),
      decisions: [...record.decisions],
      dependsOn: [...(record.spec.dependsOn ?? [])],
      group: record.spec.group,
      startedSeq: record.startedSeq,
      completedSeq: record.completedSeq,
      cancelReason: record.cancelReason,
    };
  }
}
