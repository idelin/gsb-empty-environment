/**
 * Lamport 时间戳：逻辑计数器 + 副本 ID，构成全序。
 * 用于 LWW 寄存器裁决、Map 键标签、RGA 元素标识与并发插入定序。
 */
export interface Stamp {
  readonly counter: number;
  readonly replica: string;
}

/** 全局唯一标识符，形如 "12@replicaA"。 */
export type Tag = string;

export function tagOf(stamp: Stamp): Tag {
  return `${stamp.counter}@${stamp.replica}`;
}

/** 全序比较：先比计数器，平局时比副本 ID（字典序）。 */
export function compareStamps(a: Stamp, b: Stamp): number {
  if (a.counter !== b.counter) return a.counter - b.counter;
  return a.replica < b.replica ? -1 : a.replica > b.replica ? 1 : 0;
}

export function compareTags(a: Tag, b: Tag): number {
  return compareStamps(parseTag(a), parseTag(b));
}

export function parseTag(tag: Tag): Stamp {
  const at = tag.lastIndexOf('@');
  return { counter: Number(tag.slice(0, at)), replica: tag.slice(at + 1) };
}
