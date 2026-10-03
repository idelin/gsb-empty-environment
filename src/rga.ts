/**
 * RGA (Replicated Growable Array) —— 基于因果树的协同富文本底层数组。
 *
 * 核心思想（Roh et al., 2011）：
 * - 每个字符是一个链表节点，携带全局唯一标识 id = [counter, replicaId]；
 * - 每个节点记录其因果前驱 left（即“在哪个节点之后插入”），形成因果树；
 * - 链表即因果树的前序遍历，且同一前驱下的并发子节点按 id 降序排列；
 * - 删除采用墓碑（tombstone），不真正移除节点，保证因果引用永远有效。
 *
 * 收敛性：所有副本应用同一批操作后，链表结构完全一致，
 * 与操作到达顺序无关（满足 CRDT 的强最终一致性）。
 */

/** 全局唯一标识：Lamport 式计数器 + 副本 id，按字典序比较。 */
export type RgaId = readonly [counter: number, replica: string];

/** 比较两个标识：先比 counter，再比 replica（字典序）。 */
export function compareId(a: RgaId, b: RgaId): number {
  if (a[0] !== b[0]) return a[0] - b[0];
  return a[1] < b[1] ? -1 : a[1] > b[1] ? 1 : 0;
}

function idEquals(a: RgaId | null, b: RgaId | null): boolean {
  if (a === null || b === null) return a === b;
  return a[0] === b[0] && a[1] === b[1];
}

/** 链表节点：一个字符。 */
export interface RgaNode {
  readonly id: RgaId;
  readonly char: string;
  /** 因果前驱节点的 id；null 表示插在文档头部（根）。 */
  readonly left: RgaId | null;
  /** 墓碑标记：已删除但节点保留。 */
  tombstone: boolean;
  next: RgaNode | null;
}

/** 可跨副本传输的插入操作。 */
export interface InsertOp {
  readonly type: 'insert';
  readonly id: RgaId;
  readonly left: RgaId | null;
  readonly char: string;
}

/** 可跨副本传输的删除操作。 */
export interface DeleteOp {
  readonly type: 'delete';
  readonly id: RgaId;
}

export type RgaOp = InsertOp | DeleteOp;

export class Rga {
  readonly replicaId: string;
  private counter = 0;
  /** 虚拟头节点，简化边界处理。 */
  private readonly head: RgaNode = {
    id: [0, ''],
    char: '',
    left: null,
    tombstone: false,
    next: null,
  };
  private readonly index = new Map<string, RgaNode>();

  constructor(replicaId: string) {
    this.replicaId = replicaId;
  }

  private static key(id: RgaId): string {
    return `${id[0]}@${id[1]}`;
  }

  private nextId(): RgaId {
    this.counter += 1;
    return [this.counter, this.replicaId];
  }

  /** 可见字符数（不含墓碑）。 */
  get length(): number {
    let n = 0;
    for (let node = this.head.next; node !== null; node = node.next) {
      if (!node.tombstone) n += 1;
    }
    return n;
  }

  /** 当前文档内容。 */
  toString(): string {
    let out = '';
    for (let node = this.head.next; node !== null; node = node.next) {
      if (!node.tombstone) out += node.char;
    }
    return out;
  }

  /** 第 index 个可见字符对应的节点（index 从 0 开始）。 */
  private visibleNodeAt(index: number): RgaNode | null {
    let seen = -1;
    for (let node = this.head.next; node !== null; node = node.next) {
      if (node.tombstone) continue;
      seen += 1;
      if (seen === index) return node;
    }
    return null;
  }

  /** 本地插入：在可见位置 index 处插入字符，返回可广播的操作。 */
  localInsert(index: number, char: string): InsertOp {
    if (index < 0 || index > this.length) {
      throw new RangeError(`insert index ${index} out of bounds`);
    }
    const prev = index === 0 ? null : this.visibleNodeAt(index - 1);
    const op: InsertOp = {
      type: 'insert',
      id: this.nextId(),
      left: prev === null ? null : prev.id,
      char,
    };
    this.apply(op);
    return op;
  }

  /** 本地删除：删除可见位置 index 处的字符，返回可广播的操作。 */
  localDelete(index: number): DeleteOp {
    const node = this.visibleNodeAt(index);
    if (node === null) {
      throw new RangeError(`delete index ${index} out of bounds`);
    }
    const op: DeleteOp = { type: 'delete', id: node.id };
    this.apply(op);
    return op;
  }

  /** 应用操作（本地或远程）。幂等：重复应用同一操作无副作用。 */
  apply(op: RgaOp): void {
    if (op.type === 'insert') {
      this.integrateInsert(op);
    } else {
      const node = this.index.get(Rga.key(op.id));
      if (node !== undefined) node.tombstone = true;
    }
    // Lamport 时钟推进，保证本地新 id 因果大于已知 id。
    this.counter = Math.max(this.counter, op.id[0]);
  }

  private integrateInsert(op: InsertOp): void {
    const key = Rga.key(op.id);
    if (this.index.has(key)) return; // 幂等

    const parent =
      op.left === null ? this.head : this.index.get(Rga.key(op.left));
    if (parent === undefined) {
      throw new Error(
        `causal predecessor ${Rga.key(op.left)} unknown; deliver operations in causal order`,
      );
    }

    // 因果树中同一前驱的并发子节点按 id 降序排列。
    // 从 parent 起向后跳过所有 id 更大的节点（它们是与新节点并发的
    // 兄弟节点及其子树——后代 id 必然大于祖先，故一并跳过）。
    let pos = parent;
    while (pos.next !== null && compareId(pos.next.id, op.id) > 0) {
      pos = pos.next;
    }

    const node: RgaNode = {
      id: op.id,
      char: op.char,
      left: op.left,
      tombstone: false,
      next: pos.next,
    };
    pos.next = node;
    this.index.set(key, node);
  }

  /** 按因果序合并另一副本导出的全部操作。 */
  merge(ops: readonly RgaOp[]): void {
    for (const op of ops) this.apply(op);
  }

  /** 导出当前全部操作（按链表因果序，可直接传给其他副本 merge）。 */
  exportOps(): RgaOp[] {
    const ops: RgaOp[] = [];
    for (let node = this.head.next; node !== null; node = node.next) {
      ops.push({ type: 'insert', id: node.id, left: node.left, char: node.char });
      if (node.tombstone) ops.push({ type: 'delete', id: node.id });
    }
    return ops;
  }
}
