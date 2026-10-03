/**
 * 核心 CRDT 状态机：基于操作（op-based CmRDT）的 JSON 层次对象。
 *
 * 数据模型：
 *  - Map      —— 键 -> 子值（LWW-per-key，删除用墓碑记录）
 *  - List     —— RGA 列表，元素带全局唯一 elemId，删除为墓碑标记
 *  - Register —— LWW 寄存器，保存原始 JSON 标量
 *
 * 所有节点以 nodeId 寻址，整棵树构成一张扁平的节点表，
 * 合并 = 幂等地重放对方操作日志中尚未见过的操作。
 */

export type Primitive = string | number | boolean | null;
export type Json = Primitive | Json[] | { [key: string]: Json };

/** 操作 / 元素 / 节点的全局唯一标识，格式 `${seq}:${actor}`，可全序比较。 */
export type OpId = string;
export type NodeId = string;

export const ROOT_ID: NodeId = 'root';

/** 比较两个 OpId：先比 lamport 序号，再比 actor 字典序（确定性决胜）。 */
export function compareOpIds(a: OpId, b: OpId): number {
  const ai = a.indexOf(':');
  const bi = b.indexOf(':');
  const diff = Number(a.slice(0, ai)) - Number(b.slice(0, bi));
  if (diff !== 0) return diff;
  return a < b ? -1 : a > b ? 1 : 0;
}

/** 取出 OpId 中的 lamport 序号。 */
export function opSeq(id: OpId): number {
  return Number(id.slice(0, id.indexOf(':')));
}

// ---------------------------------------------------------------------------
// 值
// ---------------------------------------------------------------------------

export interface RegisterValue {
  kind: 'register';
  value: Primitive;
  opId: OpId; // 最后一次写入该值的操作
}

export interface MapValue {
  kind: 'map';
  nodeId: NodeId;
  opId: OpId; // 创建该引用的操作
}

export interface ListValue {
  kind: 'list';
  nodeId: NodeId;
  opId: OpId;
}

export type ChildValue = RegisterValue | MapValue | ListValue;

/** 操作中携带的值描述：标量内联，容器只带新建节点的 id。 */
export type ValueSpec =
  | { kind: 'register'; value: Primitive }
  | { kind: 'map'; nodeId: NodeId }
  | { kind: 'list'; nodeId: NodeId };

// ---------------------------------------------------------------------------
// 操作
// ---------------------------------------------------------------------------

export interface MapSetOp {
  id: OpId;
  type: 'map-set';
  target: NodeId;
  key: string;
  value: ValueSpec;
}

export interface MapDeleteOp {
  id: OpId;
  type: 'map-delete';
  target: NodeId;
  key: string;
}

export interface ListInsertOp {
  id: OpId;
  type: 'list-insert';
  target: NodeId;
  elemId: OpId; // 与 id 相同，元素的唯一标识
  after: OpId | null; // 前驱元素；null 表示表头
  value: ValueSpec;
}

export interface ListDeleteOp {
  id: OpId;
  type: 'list-delete';
  target: NodeId;
  elemId: OpId;
}

export type Op = MapSetOp | MapDeleteOp | ListInsertOp | ListDeleteOp;

// ---------------------------------------------------------------------------
// 节点
// ---------------------------------------------------------------------------

export interface MapNode {
  kind: 'map';
  entries: Map<string, ChildValue>;
  /** key -> 删除它的最大 opId，用于 delete 与并发 set 的仲裁 */
  tombstones: Map<string, OpId>;
}

export interface ListElement {
  elemId: OpId;
  value: ChildValue;
  deleted: boolean;
}

export interface ListNode {
  kind: 'list';
  elements: ListElement[];
}

export type CrdtNode = MapNode | ListNode;

function emptyMapNode(): MapNode {
  return { kind: 'map', entries: new Map(), tombstones: new Map() };
}

function emptyListNode(): ListNode {
  return { kind: 'list', elements: [] };
}

// ---------------------------------------------------------------------------
// 状态机
// ---------------------------------------------------------------------------

export class Crdt {
  readonly nodes = new Map<NodeId, CrdtNode>();
  private readonly seen = new Set<OpId>();

  constructor() {
    this.nodes.set(ROOT_ID, emptyMapNode());
  }

  has(opId: OpId): boolean {
    return this.seen.has(opId);
  }

  /**
   * 应用一个操作。幂等：已见过的操作直接忽略。
   * 调用方需按因果顺序投递（同一副本的日志顺序即因果顺序）。
   * 返回是否为首次应用。
   */
  apply(op: Op): boolean {
    if (this.seen.has(op.id)) return false;
    this.seen.add(op.id);
    switch (op.type) {
      case 'map-set':
        this.applyMapSet(op);
        break;
      case 'map-delete':
        this.applyMapDelete(op);
        break;
      case 'list-insert':
        this.applyListInsert(op);
        break;
      case 'list-delete':
        this.applyListDelete(op);
        break;
    }
    return true;
  }

  private ensureContainer(spec: ValueSpec): void {
    if (spec.kind === 'register') return;
    if (!this.nodes.has(spec.nodeId)) {
      this.nodes.set(
        spec.nodeId,
        spec.kind === 'map' ? emptyMapNode() : emptyListNode(),
      );
    }
  }

  private applyMapSet(op: MapSetOp): void {
    const node = this.nodes.get(op.target);
    if (!node || node.kind !== 'map') return;

    // 该键已被更晚的操作删除 -> 本次写入过期
    const tomb = node.tombstones.get(op.key);
    if (tomb !== undefined && compareOpIds(tomb, op.id) > 0) return;

    // LWW：已存在更新（opId 更大）的值 -> 忽略
    const existing = node.entries.get(op.key);
    if (existing && compareOpIds(existing.opId, op.id) >= 0) return;

    if (op.value.kind === 'register') {
      node.entries.set(op.key, {
        kind: 'register',
        value: op.value.value,
        opId: op.id,
      });
    } else {
      this.ensureContainer(op.value);
      node.entries.set(op.key, {
        kind: op.value.kind,
        nodeId: op.value.nodeId,
        opId: op.id,
      });
    }
  }

  private applyMapDelete(op: MapDeleteOp): void {
    const node = this.nodes.get(op.target);
    if (!node || node.kind !== 'map') return;

    const existing = node.entries.get(op.key);
    if (existing && compareOpIds(existing.opId, op.id) < 0) {
      node.entries.delete(op.key);
    }
    const tomb = node.tombstones.get(op.key);
    if (tomb === undefined || compareOpIds(tomb, op.id) < 0) {
      node.tombstones.set(op.key, op.id);
    }
  }

  private applyListInsert(op: ListInsertOp): void {
    const node = this.nodes.get(op.target);
    if (!node || node.kind !== 'list') return;
    if (node.elements.some((el) => el.elemId === op.elemId)) return;

    this.ensureContainer(op.value);

    // RGA 插入：定位前驱，跳过所有标识更大的并发元素后插入，
    // 各副本对同一操作集合得到全等的线性序列。
    let index = 0;
    if (op.after !== null) {
      const at = node.elements.findIndex((el) => el.elemId === op.after);
      index = at === -1 ? 0 : at + 1;
    }
    while (
      index < node.elements.length &&
      compareOpIds(node.elements[index].elemId, op.elemId) > 0
    ) {
      index += 1;
    }

    const value: ChildValue =
      op.value.kind === 'register'
        ? { kind: 'register', value: op.value.value, opId: op.id }
        : { kind: op.value.kind, nodeId: op.value.nodeId, opId: op.id };
    node.elements.splice(index, 0, { elemId: op.elemId, value, deleted: false });
  }

  private applyListDelete(op: ListDeleteOp): void {
    const node = this.nodes.get(op.target);
    if (!node || node.kind !== 'list') return;
    const el = node.elements.find((e) => e.elemId === op.elemId);
    if (el) el.deleted = true;
  }
}
