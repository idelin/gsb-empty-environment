/**
 * RGA (Replicated Growable Array) — 基于因果树的协作文本列表。
 *
 * 每个字符是一个链表节点，携带全局唯一标识 id = (counter, site)：
 *   - counter 为 Lamport 时钟（父节点 counter + 1），保证因果后继的 id 恒大于前驱；
 *   - site 为站点标识，用于并发同值时的确定性决胜。
 * 并发在同一位置插入时，按 id 降序排列（id 大者靠前），因此所有副本收敛到同一顺序。
 */

export interface NodeId {
  /** Lamport 时钟值，因果后继严格大于其因果前驱 */
  counter: number;
  /** 站点（副本）标识 */
  site: string;
}

export type Op =
  | { kind: "insert"; id: NodeId; parentId: NodeId | null; char: string }
  | { kind: "delete"; id: NodeId };

interface RgaNode {
  id: NodeId;
  char: string;
  tombstone: boolean;
  next: RgaNode | null;
}

/** 比较两个 id：返回正数表示 a 应排在 b 之前（降序，大者优先）。 */
export function compareIds(a: NodeId, b: NodeId): number {
  if (a.counter !== b.counter) return a.counter - b.counter;
  return a.site < b.site ? -1 : a.site > b.site ? 1 : 0;
}

function idEquals(a: NodeId, b: NodeId): boolean {
  return a.counter === b.counter && a.site === b.site;
}

function idKey(id: NodeId): string {
  return `${id.counter}@${id.site}`;
}

export class RGA {
  readonly site: string;
  private clock = 0;
  /** 哨兵头节点，简化链表边界处理 */
  private readonly head: RgaNode = {
    id: { counter: -Infinity, site: "" },
    char: "",
    tombstone: true,
    next: null,
  };
  private readonly nodes = new Map<string, RgaNode>();
  /** 因果前驱尚未到达的插入操作，先挂起，父节点就绪后重放 */
  private pending: Extract<Op, { kind: "insert" }>[] = [];
  /** 目标节点尚未到达的删除操作，记录 id，节点就绪后补标墓碑 */
  private readonly deletedIds = new Set<string>();

  constructor(site: string) {
    this.site = site;
  }

  /** 本地在 visibleIndex 处插入字符，返回可广播的操作 */
  insert(visibleIndex: number, char: string): Op {
    if (char.length !== 1) throw new Error("insert expects a single character");
    const parent = this.nodeAtVisibleIndex(visibleIndex - 1);
    const parentId = parent === this.head ? null : parent.id;
    this.clock = Math.max(this.clock, parentId ? parentId.counter : 0) + 1;
    const op: Op = { kind: "insert", id: { counter: this.clock, site: this.site }, parentId, char };
    this.apply(op);
    return op;
  }

  /** 本地删除 visibleIndex 处的字符，返回可广播的操作 */
  delete(visibleIndex: number): Op {
    const node = this.nodeAtVisibleIndex(visibleIndex);
    if (node === this.head) throw new RangeError("index out of range");
    const op: Op = { kind: "delete", id: node.id };
    this.apply(op);
    return op;
  }

  /** 应用本地或远端操作（幂等） */
  apply(op: Op): void {
    if (op.kind === "insert") {
      if (this.nodes.has(idKey(op.id))) return; // 幂等：重复投递直接忽略
      const parent = op.parentId === null ? this.head : this.nodes.get(idKey(op.parentId));
      if (!parent) {
        this.pending.push(op); // 因果前驱未就绪，挂起等待
        return;
      }
      this.integrate(parent, op);
      this.clock = Math.max(this.clock, op.id.counter);
      this.flushPending();
    } else {
      const key = idKey(op.id);
      this.deletedIds.add(key); // 幂等，且覆盖删除先于插入到达的乱序场景
      const node = this.nodes.get(key);
      if (node) node.tombstone = true;
    }
  }

  /** 当前可见文本 */
  toString(): string {
    let out = "";
    for (let n = this.head.next; n; n = n.next) {
      if (!n.tombstone) out += n.char;
    }
    return out;
  }

  /** 可见长度 */
  get length(): number {
    let len = 0;
    for (let n = this.head.next; n; n = n.next) if (!n.tombstone) len++;
    return len;
  }

  /**
   * 将新节点链接到 parent 之后：
   * 跳过所有 id 大于新节点的连续后继（它们必须排在前面），
   * 使并发兄弟节点按 id 降序排列。Lamport 时钟保证因果后代 id 更大，
   * 因此跳过区间恰好覆盖"应排在新节点之前的子树"。
   */
  private integrate(parent: RgaNode, op: Extract<Op, { kind: "insert" }>): void {
    const node: RgaNode = {
      id: op.id,
      char: op.char,
      tombstone: this.deletedIds.has(idKey(op.id)),
      next: null,
    };
    let prev = parent;
    let next = parent.next;
    while (next && compareIds(next.id, op.id) > 0) {
      prev = next;
      next = next.next;
    }
    node.next = next;
    prev.next = node;
    this.nodes.set(idKey(op.id), node);
  }

  private flushPending(): void {
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (let i = 0; i < this.pending.length; i++) {
        const op = this.pending[i];
        const parent = op.parentId === null ? this.head : this.nodes.get(idKey(op.parentId));
        if (parent) {
          this.pending.splice(i, 1);
          i--;
          if (!this.nodes.has(idKey(op.id))) {
            this.integrate(parent, op);
            this.clock = Math.max(this.clock, op.id.counter);
          }
          progressed = true;
        }
      }
    }
  }

  /** 第 index 个可见字符对应的链表节点；index = -1 返回哨兵头 */
  private nodeAtVisibleIndex(index: number): RgaNode {
    let node = this.head;
    let i = -1;
    while (node.next && i < index) {
      node = node.next;
      if (!node.tombstone) i++;
    }
    if (i < index) throw new RangeError("index out of range");
    return node;
  }
}
