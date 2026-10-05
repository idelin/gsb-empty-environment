// 协议公共类型。tick 为整数；窗口 w 覆盖 tick [w*L, (w+1)*L - 1]。
// 授予在 tick <= expiresAtTick 时可用（到期 tick 含端点）。

export interface Grant {
  grantId: string;
  windowId: number;
  nodeId: string;
  amount: number;
  issuedTick: number;
  expiresAtTick: number;
}

export interface Alloc {
  grantId: string;
  amount: number;
}

export type RejectReason = 'no-active-grants' | 'insufficient-quota' | 'non-positive-cost';

export type SpendResult =
  | { status: 'accepted'; requestId: string; cost: number; windowId: number; allocs: Alloc[]; dedup: boolean }
  | { status: 'rejected'; requestId: string; cost: number; reason: RejectReason; dedup: boolean }
  | { status: 'conflict'; requestId: string; cost: number; originalCost: number };

export interface GrantMsg {
  kind: 'grant';
  grant: Grant;
}

export interface ReturnMsg {
  kind: 'return';
  returnId: string;
  grantId: string;
  nodeId: string;
  unspent: number;
  sentTick: number;
}

export interface ReturnConfirmMsg {
  kind: 'return-confirm';
  returnId: string;
  grantId: string;
  nodeId: string;
  unspent: number;
}

export type Msg = GrantMsg | ReturnMsg | ReturnConfirmMsg;
export type MsgKind = Msg['kind'];

export function describeMsg(msg: Msg): string {
  switch (msg.kind) {
    case 'grant': {
      const g = msg.grant;
      return `grant ${g.grantId} node=${g.nodeId} win=${g.windowId} amt=${g.amount} exp=${g.expiresAtTick}`;
    }
    case 'return':
      return `return ${msg.returnId} g=${msg.grantId} unspent=${msg.unspent}`;
    case 'return-confirm':
      return `confirm ${msg.returnId} g=${msg.grantId} unspent=${msg.unspent}`;
  }
}
