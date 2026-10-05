// 消息身份: (node, epoch, counter)。每个 epoch 内 counter 从 1 开始连续编号。
export interface MessageId {
  node: string;
  epoch: number;
  counter: number;
}

// 连续前缀表: "node@epoch" -> 已连续观察到的最大 counter。
export type PrefixTable = Record<string, number>;

// 消息携带发送方已观察到的各成员连续前缀作为因果依赖。
export interface Message {
  id: MessageId;
  deps: PrefixTable;
  payload: string;
}

// 回收区间: [from, to] 表示某前缀下被回收的 counter 闭区间。
export interface Reclamation {
  kind: "dedup" | "tombstone";
  prefix: string;
  from: number;
  to: number;
}

export function prefixKey(node: string, epoch: number): string {
  return `${node}@${epoch}`;
}

export function idKey(id: MessageId): string {
  return `${id.node}@${id.epoch}#${id.counter}`;
}

export function parseIdKey(key: string): MessageId {
  const hash = key.lastIndexOf("#");
  const at = key.lastIndexOf("@", hash);
  return {
    node: key.slice(0, at),
    epoch: Number(key.slice(at + 1, hash)),
    counter: Number(key.slice(hash + 1)),
  };
}
