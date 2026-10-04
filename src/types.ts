/**
 * 审批计划库 —— 类型定义
 *
 * 概念模型：
 * - 计划(Plan)由若干审批节点(Node)构成 DAG，节点间通过 dependsOn 表达前后依赖。
 * - 节点签署模式：'any' 或签（任一审批人通过即通过），'all' 会签（全员通过才通过）。
 * - 或签组(OrGroup)：多个分支节点共享同一 group id，任一分支通过则整组通过，
 *   其余未开始分支自动取消；组外节点可依赖组 id，不受分支取消影响。
 */

export type NodeStatus =
  | 'pending'      // 待就绪（前置未满足或未被启动）
  | 'in_progress'  // 审批中
  | 'approved'     // 已通过（终态，不可撤回）
  | 'rejected'     // 已拒绝（终态）
  | 'cancelled';   // 已取消（终态，仅未开始节点可被取消）

export type SignMode = 'any' | 'all';

export interface NodeSpec {
  id: string;
  name?: string;
  /** 签署模式：any=或签，all=会签 */
  mode: SignMode;
  /** 审批人列表 */
  approvers: string[];
  /** 前置依赖：节点 id 或或签组 id，全部满足后本节点才可执行 */
  dependsOn?: string[];
  /** 所属或签组 id（该节点为组内一条分支） */
  group?: string;
}

export interface PlanSpec {
  nodes: NodeSpec[];
}

export interface Decision {
  approverId: string;
  decision: 'approved' | 'rejected';
  /** 逻辑时钟序号，用于刻画既有执行顺序 */
  seq: number;
}

export interface NodeSnapshot {
  id: string;
  name?: string;
  mode: SignMode;
  status: NodeStatus;
  approvers: string[];
  /** 尚未给出决定的审批人（或签通过后其余人自动清空） */
  pendingApprovers: string[];
  decisions: Decision[];
  dependsOn: string[];
  group?: string;
  startedSeq: number | null;
  completedSeq: number | null;
  cancelReason?: string;
}

/** 计划修订请求。addDependency: { from, to } 表示 from 新增前置 to（to 须先于 from 完成）。 */
export interface PlanRevision {
  addNodes?: NodeSpec[];
  removeNodes?: string[];
  addDependencies?: { from: string; to: string }[];
  removeDependencies?: { from: string; to: string }[];
}

export interface RejectedChange {
  change: string;
  reason: string;
}

export interface RevisionReport {
  applied: string[];
  rejected: RejectedChange[];
}
