/**
 * 文档层：以路径寻址的分层 JSON CRDT。
 *
 *   const a = JsonCRDT.create('A', { list: [{ title: 'x', tags: ['ts'] }] });
 *   const b = a.fork('B');
 *   a.set(['list', 0, 'title'], 'y');
 *   b.push(['list', 0, 'tags'], 'go');
 *   a.merge(b); // 两个并发修改都被保留，且 list[0] 节点引用不变
 */
import { ArrayCRDT, MapCRDT, LWWRegister, nodeFrom } from './nodes.ts';
import type { CRDTNode, JsonObject, JsonValue } from './nodes.ts';
import type { Stamp } from './stamp.ts';

export type PathSegment = string | number;
export type Path = readonly PathSegment[];

export class JsonCRDT {
  readonly replica: string;
  private counter = 0;
  private root: MapCRDT;

  private constructor(replica: string) {
    this.replica = replica;
    this.root = new MapCRDT();
  }

  static create(replica: string, initial: JsonObject = {}): JsonCRDT {
    const doc = new JsonCRDT(replica);
    for (const [key, value] of Object.entries(initial)) {
      doc.root.set(key, nodeFrom(value, () => doc.tick()), doc.tick());
    }
    return doc;
  }

  /** 派生新副本：深拷贝当前状态，使用新的副本 ID。 */
  fork(replica: string): JsonCRDT {
    const doc = new JsonCRDT(replica);
    doc.root = this.root.clone() as MapCRDT;
    doc.counter = this.counter;
    return doc;
  }

  private tick(): Stamp {
    return { counter: ++this.counter, replica: this.replica };
  }

  /* ------------------------------ 读取 ------------------------------ */

  value(): JsonObject {
    return this.root.value();
  }

  /** 按路径取 CRDT 节点（返回的是活引用，可用于合并前后的引用相等性断言）。 */
  nodeAt(path: Path): CRDTNode | undefined {
    let node: CRDTNode | undefined = this.root;
    for (const seg of path) {
      if (node instanceof MapCRDT && typeof seg === 'string') {
        node = node.get(seg);
      } else if (node instanceof ArrayCRDT && typeof seg === 'number') {
        node = node.at(seg);
      } else {
        return undefined;
      }
    }
    return node;
  }

  /** 按路径取物化 JSON 值。 */
  get(path: Path): JsonValue | undefined {
    return this.nodeAt(path)?.value();
  }

  /* ------------------------------ 写入 ------------------------------ */

  /**
   * 设置路径处的值。
   * 原始值写入已存在的 Register 时原地更新（保留节点引用）；
   * 其余情况替换该位置的子树。
   */
  set(path: Path, value: JsonValue): void {
    if (path.length === 0) throw new Error('cannot replace document root');
    const parent = this.nodeAt(path.slice(0, -1));
    const key = path[path.length - 1];
    if (parent instanceof MapCRDT && typeof key === 'string') {
      const existing = parent.get(key);
      const primitive = value === null || typeof value !== 'object';
      if (existing instanceof LWWRegister && primitive) {
        existing.set(value, this.tick());
        return;
      }
      parent.set(key, nodeFrom(value, () => this.tick()), this.tick());
      return;
    }
    if (parent instanceof ArrayCRDT && typeof key === 'number') {
      const id = parent.idAt(key);
      if (id === undefined) throw new Error(`array index out of range: ${key}`);
      parent.removeById(id);
      parent.insertAfter(id, nodeFrom(value, () => this.tick()), this.tick());
      return;
    }
    throw new Error(`invalid path: ${path.join('.')}`);
  }

  /** 向路径处的数组追加元素。 */
  push(path: Path, value: JsonValue): void {
    const node = this.nodeAt(path);
    if (!(node instanceof ArrayCRDT)) throw new Error(`not an array: ${path.join('.')}`);
    node.push(nodeFrom(value, () => this.tick()), this.tick());
  }

  /** 向路径处的数组指定下标插入元素。 */
  insert(path: Path, index: number, value: JsonValue): void {
    const node = this.nodeAt(path);
    if (!(node instanceof ArrayCRDT)) throw new Error(`not an array: ${path.join('.')}`);
    const parentId = index <= 0 ? null : node.idAt(index - 1) ?? null;
    node.insertAfter(parentId, nodeFrom(value, () => this.tick()), this.tick());
  }

  /** 删除 Map 键或数组下标。 */
  remove(path: Path): boolean {
    if (path.length === 0) return false;
    const parent = this.nodeAt(path.slice(0, -1));
    const key = path[path.length - 1];
    if (parent instanceof MapCRDT && typeof key === 'string') return parent.delete(key);
    if (parent instanceof ArrayCRDT && typeof key === 'number') {
      const id = parent.idAt(key);
      return id !== undefined && parent.removeById(id);
    }
    return false;
  }

  /* ------------------------------ 合并 ------------------------------ */

  /**
   * 原地三向合并：把 other 的状态并入本文档。
   * 已存在的节点对象一律就地更新，不会被替换，
   * 满足交换律 / 结合律 / 幂等律。
   */
  merge(other: JsonCRDT): this {
    this.root.merge(other.root);
    this.counter = Math.max(this.counter, other.counter);
    return this;
  }
}
