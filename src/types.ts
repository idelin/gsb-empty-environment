/**
 * 节点生命周期状态机：
 *   Ready --(发起/收到 LeaveIntent)--> Leaving --(平静期超时)--> 物理移除
 *
 * 注意：Node.js 原生类型擦除不支持 enum，故使用 const 对象 + 联合类型。
 */
export const NodeState = {
  Ready: "Ready",
  Leaving: "Leaving",
} as const;

export type NodeState = (typeof NodeState)[keyof typeof NodeState];

/** 离开意向消息：由发起节点广播给所有对等节点 */
export interface LeaveIntent {
  readonly kind: "LeaveIntent";
  readonly nodeId: string;
  readonly epoch: number;
  readonly sentAt: number;
}

/** 路由表中单个节点的条目 */
export interface RouteEntry {
  readonly nodeId: string;
  state: NodeState;
  /** 当前在途（in-flight）请求数，用于排空（drain）统计 */
  inflight: number;
  /** 平静期定时器句柄（仅 Leaving 状态存在） */
  quiescentTimer?: ReturnType<typeof setTimeout>;
}

/** 模拟 RPC 请求 */
export interface RpcRequest {
  readonly id: string;
  readonly payload: string;
}

export interface RpcResponse {
  readonly id: string;
  readonly from: string;
  readonly ok: boolean;
}
