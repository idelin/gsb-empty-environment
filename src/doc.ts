/**
 * Doc：面向用户的协同文档。
 *
 * - `doc.root` 返回根 Map 的 Proxy，可像普通 JS 对象一样嵌套读写：
 *     doc.root.list[0].title = 'x'
 *     doc.root.list[0].tags.push('go')
 * - 每次变更生成操作并追加到本地日志 `doc.log`；
 * - `a.mergeFrom(b)` 把 b 的日志中 a 尚未见过的操作幂等重放进来，
 *   双向 mergeFrom 即完成一次三向合并（共同祖先 + 双方变更）。
 * - Proxy 按 nodeId 缓存：合并只就地更新节点状态，
 *   已持有的子对象引用（如 root.list[0]）在合并前后保持同一对象。
 */

import {
  Crdt,
  ROOT_ID,
  opSeq,
  type ChildValue,
  type Json,
  type ListNode,
  type MapNode,
  type NodeId,
  type Op,
  type OpId,
  type Primitive,
  type ValueSpec,
} from './crdt.ts';

const NODE_ID: unique symbol = Symbol('crdt.nodeId');

export class Doc {
  readonly actor: string;
  readonly crdt = new Crdt();
  /** 本地已知全部操作的因果有序日志（含合并进来的远端操作）。 */
  readonly log: Op[] = [];
  private seq = 0;
  private readonly proxies = new Map<NodeId, unknown>();

  constructor(actor: string) {
    this.actor = actor;
  }

  /** 根 Map 的读写代理。 */
  get root(): any {
    return this.wrap(ROOT_ID);
  }

  // -------------------------------------------------------------------------
  // 操作生成
  // -------------------------------------------------------------------------

  private nextId(): OpId {
    this.seq += 1;
    return `${this.seq}:${this.actor}`;
  }

  private emit(op: Op): void {
    this.log.push(op);
    this.crdt.apply(op);
  }

  /** 为 JSON 值生成 ValueSpec；容器类型会预分配 nodeId。 */
  private specOf(value: Json): ValueSpec {
    if (value === null || typeof value !== 'object') {
      return { kind: 'register', value: value as Primitive };
    }
    return Array.isArray(value)
      ? { kind: 'list', nodeId: this.nextId() }
      : { kind: 'map', nodeId: this.nextId() };
  }

  /** 把普通 JSON 值递归物化为 CRDT 子树（父操作先于子操作发出）。 */
  private fill(nodeId: NodeId, value: Json): void {
    if (Array.isArray(value)) {
      for (const item of value) this.pushRaw(nodeId, item);
    } else if (value !== null && typeof value === 'object') {
      for (const [key, item] of Object.entries(value)) {
        this.setRaw(nodeId, key, item);
      }
    }
  }

  private setRaw(target: NodeId, key: string, value: Json): void {
    const spec = this.specOf(value);
    this.emit({ id: this.nextId(), type: 'map-set', target, key, value: spec });
    if (spec.kind !== 'register') this.fill(spec.nodeId, value);
  }

  private deleteRaw(target: NodeId, key: string): void {
    this.emit({ id: this.nextId(), type: 'map-delete', target, key });
  }

  private insertRaw(target: NodeId, after: OpId | null, value: Json): OpId {
    const spec = this.specOf(value);
    const elemId = this.nextId();
    this.emit({ id: elemId, type: 'list-insert', target, elemId, after, value: spec });
    if (spec.kind !== 'register') this.fill(spec.nodeId, value);
    return elemId;
  }

  private pushRaw(target: NodeId, value: Json): OpId {
    const node = this.crdt.nodes.get(target) as ListNode;
    const visible = node.elements.filter((el) => !el.deleted);
    const after = visible.length === 0 ? null : visible[visible.length - 1].elemId;
    return this.insertRaw(target, after, value);
  }

  // -------------------------------------------------------------------------
  // 合并
  // -------------------------------------------------------------------------

  /** 把 other 的操作日志中本副本尚未见过的操作按因果序重放进来。幂等。 */
  mergeFrom(other: Doc): void {
    for (const op of other.log) {
      if (this.crdt.apply(op)) {
        this.log.push(op);
        // lamport 时钟：本地序号始终不小于任何已见操作
        const seq = opSeq(op.id);
        if (seq > this.seq) this.seq = seq;
      }
    }
  }

  /** 双向合并：a、b 各自吸收对方的变更，收敛到同一状态。 */
  static merge(a: Doc, b: Doc): void {
    a.mergeFrom(b);
    b.mergeFrom(a);
  }

  /** 以当前日志派生一个新副本（可指定新 actor）。 */
  fork(actor: string): Doc {
    const doc = new Doc(actor);
    doc.mergeFrom(this);
    return doc;
  }

  // -------------------------------------------------------------------------
  // 读取
  // -------------------------------------------------------------------------

  private unwrap(child: ChildValue): unknown {
    return child.kind === 'register' ? child.value : this.wrap(child.nodeId);
  }

  private listElements(nodeId: NodeId) {
    const node = this.crdt.nodes.get(nodeId) as ListNode;
    return node.elements.filter((el) => !el.deleted);
  }

  /** 物化为普通 JSON（深拷贝快照）。 */
  toJSON(nodeId: NodeId = ROOT_ID): Json {
    const node = this.crdt.nodes.get(nodeId);
    if (!node) return null;
    if (node.kind === 'map') {
      const out: { [key: string]: Json } = {};
      for (const [key, child] of node.entries) {
        out[key] =
          child.kind === 'register' ? child.value : this.toJSON(child.nodeId);
      }
      return out;
    }
    return node.elements
      .filter((el) => !el.deleted)
      .map((el) =>
        el.value.kind === 'register'
          ? el.value.value
          : this.toJSON(el.value.nodeId),
      );
  }

  // -------------------------------------------------------------------------
  // Proxy
  // -------------------------------------------------------------------------

  private wrap(nodeId: NodeId): any {
    const cached = this.proxies.get(nodeId);
    if (cached !== undefined) return cached;
    const node = this.crdt.nodes.get(nodeId);
    if (!node) throw new Error(`unknown node: ${nodeId}`);
    const proxy = node.kind === 'map' ? this.mapProxy(nodeId) : this.listProxy(nodeId);
    this.proxies.set(nodeId, proxy);
    return proxy;
  }

  private mapProxy(nodeId: NodeId): any {
    const self = this;
    return new Proxy(Object.create(null), {
      get(_target, prop) {
        if (prop === NODE_ID) return nodeId;
        if (prop === 'toJSON') return () => self.toJSON(nodeId);
        if (typeof prop !== 'string') return undefined;
        const node = self.crdt.nodes.get(nodeId) as MapNode;
        const child = node.entries.get(prop);
        return child === undefined ? undefined : self.unwrap(child);
      },
      set(_target, prop, value) {
        if (typeof prop !== 'string') return false;
        self.setRaw(nodeId, prop, value as Json);
        return true;
      },
      deleteProperty(_target, prop) {
        self.deleteRaw(nodeId, String(prop));
        return true;
      },
      ownKeys() {
        const node = self.crdt.nodes.get(nodeId) as MapNode;
        return [...node.entries.keys()];
      },
      getOwnPropertyDescriptor(_target, prop) {
        const node = self.crdt.nodes.get(nodeId) as MapNode;
        if (!node.entries.has(prop as string)) return undefined;
        return { enumerable: true, configurable: true, writable: true };
      },
      has(_target, prop) {
        const node = self.crdt.nodes.get(nodeId) as MapNode;
        return node.entries.has(prop as string);
      },
    });
  }

  private listProxy(nodeId: NodeId): any {
    const self = this;
    const snapshot = (): unknown[] =>
      self.listElements(nodeId).map((el) => self.unwrap(el.value));

    return new Proxy(Object.create(null), {
      get(_target, prop) {
        if (prop === NODE_ID) return nodeId;
        if (prop === Symbol.iterator) {
          return function* () {
            yield* snapshot();
          };
        }
        if (prop === 'toArray' || prop === 'toJSON') return () => snapshot();
        if (prop === 'push') {
          return (...values: Json[]) => {
            for (const value of values) self.pushRaw(nodeId, value);
            return self.listElements(nodeId).length;
          };
        }
        if (prop === 'insertAt') {
          return (index: number, value: Json) => {
            const visible = self.listElements(nodeId);
            if (index < 0 || index > visible.length) {
              throw new RangeError(`insertAt: index ${index} out of range`);
            }
            const after = index === 0 ? null : visible[index - 1].elemId;
            self.insertRaw(nodeId, after, value);
          };
        }
        if (prop === 'deleteAt') {
          return (index: number) => {
            const visible = self.listElements(nodeId);
            const el = visible[index];
            if (!el) throw new RangeError(`deleteAt: index ${index} out of range`);
            self.emit({
              id: self.nextId(),
              type: 'list-delete',
              target: nodeId,
              elemId: el.elemId,
            });
          };
        }
        if (typeof prop === 'string' && /^(0|[1-9]\d*)$/.test(prop)) {
          const el = self.listElements(nodeId)[Number(prop)];
          return el === undefined ? undefined : self.unwrap(el.value);
        }
        // 其余属性（length、forEach、map、slice……）委托给只读快照数组
        const arr = snapshot() as any;
        const value = arr[prop];
        return typeof value === 'function' ? value.bind(arr) : value;
      },
      set(_target, prop, value) {
        // 下标赋值 = 删除旧元素 + 同位插入新元素（两步操作，并发时表现为多值并存）
        if (typeof prop === 'string' && /^(0|[1-9]\d*)$/.test(prop)) {
          const visible = self.listElements(nodeId);
          const index = Number(prop);
          const el = visible[index];
          if (el === undefined) return false;
          self.emit({
            id: self.nextId(),
            type: 'list-delete',
            target: nodeId,
            elemId: el.elemId,
          });
          const rest = self.listElements(nodeId);
          const after = index === 0 ? null : rest[index - 1].elemId;
          self.insertRaw(nodeId, after, value as Json);
          return true;
        }
        return false;
      },
      ownKeys() {
        return self.listElements(nodeId).map((_el, i) => String(i));
      },
      getOwnPropertyDescriptor(_target, prop) {
        if (typeof prop === 'string' && /^(0|[1-9]\d*)$/.test(prop)) {
          const index = Number(prop);
          if (index < self.listElements(nodeId).length) {
            return { enumerable: true, configurable: true, writable: true };
          }
        }
        return undefined;
      },
    });
  }
}
