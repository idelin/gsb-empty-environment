/** 失效消息：告知节点某个 key 已更新到 version，旧副本在屏障前仍可服务。 */
export interface InvalidateMessage {
  kind: "invalidate";
  key: string;
  version: number;
}

/** 屏障消息：发布后，节点只能返回 version >= 该版本的数据，否则必须等待。 */
export interface BarrierMessage {
  kind: "barrier";
  key: string;
  version: number;
}

export type Message = InvalidateMessage | BarrierMessage;

/** 总线上的消息信封，带全局单调递增序号，用于断线重放。 */
export type EnvelopedMessage = Message & { seq: number };

export type MessageHandler = (msg: EnvelopedMessage) => void;
