/**
 * 可逆编辑撤销树（Undo/Redo CRDT Graph）—— 核心类型定义
 *
 * 模型：
 *  - 文档底层是 RGA（Replicated Growable Array）字符序列，删除即墓碑标记；
 *  - 每个操作（含撤销/重做产生的反向补偿操作）都是 DAG 中的一个节点，
 *    节点通过 parents 指向创建时的因果头部（heads），保证拓扑有效性；
 *  - Undo/Redo 不修改历史，只追加"反向补偿节点"（insert↔delete↔restore），
 *    因此只影响操作者自己的字符，不会破坏其他协作者的并发输入。
 */

/** 字符唯一标识：`${site}:${counter}`，按 (counter, site) 字典序比较 */
export type CharId = string;

/** 操作节点唯一标识：`node:${site}:${counter}` */
export type NodeId = string;

/** 站点（协作者）标识 */
export type SiteId = string;

export interface InsertPayload {
  /** 插入文本（一个节点可携带多个连续字符） */
  text: string;
  /** 插入位置：左侧字符的 id，null 表示文档开头 */
  after: CharId | null;
  /** 本节点分配的字符 id 区间起点（counter），字符 id 为 site:base .. site:base+len-1 */
  base: number;
}

export interface DeletePayload {
  /** 目标字符 id 集合 */
  targets: CharId[];
}

export interface RestorePayload {
  /** 目标字符 id 集合 */
  targets: CharId[];
}

export type OpKind = 'insert' | 'delete' | 'restore';

export interface OpNode {
  id: NodeId;
  author: SiteId;
  kind: OpKind;
  /** 因果父节点（创建时 DAG 的 heads），维护拓扑序 */
  parents: NodeId[];
  /** 若为补偿节点，指向被补偿的节点 */
  compensates: NodeId | null;
  payload: InsertPayload | DeletePayload | RestorePayload;
}
