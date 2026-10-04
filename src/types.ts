/** 节点标识 */
export type NodeId = string;

/**
 * LSA 版本 = (代次 generation, 序号 sequence)，按字典序比较。
 * - 同源普通更新：序号 +1；
 * - 删除后重建：代次 +1、序号归零，避免与网络中残留的旧代次副本混淆。
 */
export interface Version {
  generation: number;
  sequence: number;
}

export function compareVersions(a: Version, b: Version): number {
  if (a.generation !== b.generation) return a.generation - b.generation;
  return a.sequence - b.sequence;
}

export function formatVersion(v: Version): string {
  return `${v.generation}.${v.sequence}`;
}

/** 链路状态通告：每个来源（origin）一条，描述其直连邻居及开销 */
export interface Lsa {
  origin: NodeId;
  version: Version;
  links: Record<NodeId, number>;
  withdrawn: boolean;
}

/** 摘要 = 逐来源版本向量。关键：按来源分别记录进度，而非全局最大版本。 */
export type VersionVector = Record<NodeId, Version>;

export type Message =
  | { kind: "summary"; from: NodeId; to: NodeId; vector: VersionVector }
  | { kind: "request"; from: NodeId; to: NodeId; origins: NodeId[] }
  | { kind: "update"; from: NodeId; to: NodeId; lsas: Lsa[] };

export function clone<T>(x: T): T {
  return JSON.parse(JSON.stringify(x)) as T;
}
