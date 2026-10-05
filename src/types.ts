// 审批计划库的基础类型定义。

// 签署模式：any = 或签（任一人同意即通过），all = 会签（所有人同意才通过）。
export type SignMode = 'any' | 'all';

// 节点状态：pending 待就绪 / ready 可执行 / approved 已通过 / rejected 已否决 / cancelled 已取消。
export type NodeStatus = 'pending' | 'ready' | 'approved' | 'rejected' | 'cancelled';

export type Decision = 'approve' | 'reject';

export interface NodeDef {
  id: string;
  label: string;
  mode: SignMode;
  assignees: string[];
  deps: string[];
}

export interface Signature {
  user: string;
  decision: Decision;
  tick: number;
}

export interface NodeState {
  def: NodeDef;
  status: NodeStatus;
  signatures: Signature[];
  cancelledAssignees: string[];
  completedTick: number;
  note: string;
}

export interface OpResult {
  ok: boolean;
  error: string;
}
