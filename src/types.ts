export type NodeState = 'pending' | 'running' | 'completed' | 'failed' | 'cancelled';

export interface TaskContext {
  signal: AbortSignal;
}

export type TaskFn<T = unknown> = (ctx: TaskContext) => T | Promise<T>;

export interface FailureRecord {
  node: number;
  error: unknown;
}

export interface CancelRecord {
  node: number;
  kind: 'explicit' | 'dependency';
  message: string;
  /** 导致本次撤销的依赖节点编号（失败或已撤销），升序排列 */
  sources: number[];
  /** 主因：sources 中编号最小的节点 */
  primarySource?: number;
  /** 原因链：主因节点自身的失败/撤销记录 */
  cause?: FailureRecord | CancelRecord;
}

export interface NodeRecord<T = unknown> {
  id: number;
  name: string;
  state: NodeState;
  result?: T;
  failure?: FailureRecord;
  cancellation?: CancelRecord;
}
