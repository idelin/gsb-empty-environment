import type {
  AccessorProperty,
  DataProperty,
  GraphNode,
  NativeProperty,
  ObjectGraph,
  SlotValue,
  XrayCallImpl,
} from './graph.ts';
import { isRef } from './graph.ts';

const NODE_ID: unique symbol = Symbol('xray.node-id');
const XRAY_OWNER: unique symbol = Symbol('xray.owner');
const XRAY_OWNERS = new WeakMap<object, Xray>();
const XRAY_BY_NODE = new Map<string, Xray>();

interface ViewHolder {
  [NODE_ID]: string;
  [XRAY_OWNER]: Xray;
}

export type XrayView = object;

interface NativeLookup {
  readonly nodeId: string;
  readonly key: string;
  readonly prop: NativeProperty;
}

/**
 * 访问器属性的报告：视图读取访问器不会调用内容域函数，
 * 只返回一个冻结的存在性标记。
 */
export interface AccessorReport {
  readonly xrayAccessor: true;
  readonly ownerNode: string;
  readonly key: string;
}

const isCallable = (node: GraphNode): boolean => node.intrinsic === 'Function';

const hasOwnProp = (record: object | undefined, key: string): boolean =>
  record !== undefined && Object.prototype.hasOwnProperty.call(record, key);

export class Xray {
  readonly graph: ObjectGraph;
  private readonly views = new Map<string, object>();
  private readonly accessorMarkers = new Map<string, Map<string, object>>();
  constructor(graph: ObjectGraph) {
    for (const node of Object.values(graph.nodes)) {
      if (graph.nodes[node.id] !== node) {
        throw new TypeError(`duplicate object id: ${node.id}`);
      }
      const checkPointer = (pointer: string | null | undefined, label: string) => {
        if (pointer !== undefined && pointer !== null && !graph.nodes[pointer]) {
          throw new TypeError(`${label} of ${node.id} points to missing node ${pointer}`);
        }
      };
      checkPointer(node.nativeProto, 'nativeProto');
      checkPointer(node.contentProto, 'contentProto');
      for (const prop of Object.values(node.nativeProps ?? {})) {
        if (prop.kind === 'data') this.checkValue(graph, prop.value, node.id);
      }
      for (const prop of Object.values(node.contentProps ?? {})) {
        if (prop.kind === 'data') this.checkValue(graph, prop.value, node.id);
      }
      for (const slot of node.arraySlots ?? []) {
        this.checkValue(graph, slot, node.id);
      }
    }
    this.graph = graph;
    for (const node of Object.values(graph.nodes)) XRAY_BY_NODE.set(node.id, this);
  }

  private checkValue(graph: ObjectGraph, value: SlotValue, ownerId: string): void {
    if (isRef(value) && !graph.nodes[value.$ref]) {
      throw new TypeError(`node ${ownerId} references missing node ${value.$ref}`);
    }
    if (Array.isArray(value)) {
      for (const item of value) this.checkValue(graph, item, ownerId);
    }
  }

  private node(id: string): GraphNode {
    const node = this.graph.nodes[id];
    if (!node) throw new TypeError(`unknown object id: ${id}`);
    return node;
  }

 /** 入口：把图中的对象包成 Xray 视图。 */
  view(id: string): object {
    this.node(id);
    const cached = this.views.get(id);
    if (cached) return cached;

    const node = this.node(id);
    // 普通函数表达式保留 this 绑定（call/apply/bind 依赖方法形式调用的 this），
    // length/name 可配置，让 Proxy 不变量与视图过滤逻辑保持一致。
    const target = isCallable(node)
      ? (function () {}) as unknown as ViewHolder & (() => void)
      : {} as ViewHolder;
    Object.defineProperty(target, NODE_ID, { value: id });
    Object.defineProperty(target, XRAY_OWNER, { value: this });
    if (node.arraySlots) {
      Object.defineProperty(target, 'length', {
        value: node.arraySlots.length,
        enumerable: false,
        configurable: false,
        writable: true,
      });
    }

    const view = new Proxy(target, this.handler) as unknown as object;
    XRAY_OWNERS.set(view, this);
    // 先缓存再返回：环上的回边会命中这里，展开永远不会递归回自己。
    this.views.set(id, view);
    return view;
  }

  private readonly handler: ProxyHandler<ViewHolder & (() => void)> = {
    get: (target, key, receiver) => {
      if (typeof key === 'symbol') {
        if (key === NODE_ID || key === XRAY_OWNER) {
          return target[key];
        }
        if (key === Symbol.iterator) {
          const node = this.node(target[NODE_ID]);
          if (node.arraySlots) return () => this.iterateSlots(target[NODE_ID]);
        }
        // 符号键只可能来自目标自身；用 receiver 转发会让函数目标丢失 this 绑定。
        return (target as Record<symbol, unknown>)[key];
      }
      const id = target[NODE_ID];
      // __proto__ 是内容域最常替换的入口，视图强制按原生原型走。
      if (key === '__proto__') {
        return this.nativeProtoView(id);
      }
      const arrayNode = this.node(id);
      if (key === 'length' && arrayNode.arraySlots) {
        return arrayNode.arraySlots.length;
      }
      const slotIndex = this.slotIndex(id, key);
      if (slotIndex !== undefined) {
        return this.wrapValue(arrayNode.arraySlots![slotIndex]);
      }
      const found = this.lookupNative(id, key);
      if (!found) return undefined;
      return this.readProperty(found);
    },

    has: (target, key) => {
      if (typeof key === 'symbol') return Reflect.has(target, key);
      if (key === '__proto__') return this.node(target[NODE_ID]).nativeProto != null;
      if (key === 'length' && this.node(target[NODE_ID]).arraySlots) return true;
      if (this.slotIndex(target[NODE_ID], key) !== undefined) return true;
      return this.lookupNative(target[NODE_ID], key) !== undefined;
    },

    ownKeys: (target) => {
      const id = target[NODE_ID];
      const keys: (string | symbol)[] = [];
      const seen = new Set<string | symbol>();
      const push = (key: string | symbol) => {
        if (!seen.has(key)) {
          seen.add(key);
          keys.push(key);
        }
      };
      const node = this.node(id);
      for (let index = 0; index < (node.arraySlots?.length ?? 0); index += 1) {
        push(String(index));
      }
      if (node.arraySlots) push('length');
      for (const key of Object.keys(node.nativeProps ?? {})) push(key);
      // Proxy 不变量：ownKeys 必须包含目标上不可配置的自有键。
      for (const ownKey of Reflect.ownKeys(target)) {
        const descriptor = Object.getOwnPropertyDescriptor(target, ownKey);
        if (descriptor && !descriptor.configurable) push(ownKey);
      }
      return keys;
    },

    getOwnPropertyDescriptor: (target, key) => {
      if (typeof key === 'symbol') return Reflect.getOwnPropertyDescriptor(target, key);
      const id = target[NODE_ID];
      const node = this.node(id);
      if (key === 'length' && node.arraySlots) {
        return {
          enumerable: false,
          configurable: false,
          writable: true,
          value: node.arraySlots.length,
        };
      }
      const slotIndex = this.slotIndex(id, key);
      if (slotIndex !== undefined) {
        return {
          enumerable: true,
          configurable: true,
          writable: false,
          value: this.wrapValue(node.arraySlots![slotIndex]),
        };
      }
      const found = this.lookupOwnNative(id, key);
      if (!found) return undefined;
      return this.descriptorFor(found);
    },

    defineProperty: () => {
      throw new TypeError('Xray views are read-only');
    },

    set: () => {
      throw new TypeError('Xray views are read-only');
    },

    deleteProperty: () => {
      throw new TypeError('Xray views are read-only');
    },

    preventExtensions: () => {
      throw new TypeError('Xray views are read-only');
    },

    getPrototypeOf: (target) => {
      const nativeProto = this.node(target[NODE_ID]).nativeProto;
      if (nativeProto === null || nativeProto === undefined) return null;
      return this.view(nativeProto);
    },

    apply: (target, thisArg, args) => {
      const node = this.node(target[NODE_ID]);
      if (!isCallable(node) || !node.privilegedCall) {
        throw new TypeError(`${node.id} is not callable through the Xray`);
      }
      const impl: XrayCallImpl = node.privilegedCall;
      return impl(thisArg, args, node.id);
    },
  };

  private descriptorFor(found: NativeLookup): PropertyDescriptor | undefined {
    const prop = found.prop;
    const enumerable = prop.enumerable ?? true;
    const configurable = prop.configurable ?? true;
    if (prop.kind === 'data') {
      const data = prop as DataProperty;
      return {
        enumerable,
        configurable,
        writable: data.writable ?? true,
        value: this.readProperty(found),
      };
    }
    const accessor = prop as AccessorProperty;
    const report: AccessorReport = {
      xrayAccessor: true,
      ownerNode: found.nodeId,
      key: found.key,
    };
    // 提供安全的描述符形态：get/set 是特权侧 thunk，绝不指向内容域函数。
    return {
      enumerable,
      configurable,
      get: () => this.accessorMarker(report),
      set: accessor.configurable === false ? undefined : () => {
        throw new TypeError('Xray views are read-only');
      },
    };
  }

  private accessorMarker(report: AccessorReport): object {
    const perNode = this.accessorMarkers.get(report.ownerNode);
    const cached = perNode?.get(report.key);
    if (cached) return cached;

    const marker = Object.freeze({ ...report });
    let nodeCache = perNode;
    if (!nodeCache) {
      nodeCache = new Map<string, object>();
      this.accessorMarkers.set(report.ownerNode, nodeCache);
    }
    nodeCache.set(report.key, marker);
    return marker;
  }

  /** 沿原生原型链查找自有原生属性；访问过的节点集合防止畸形链成环。 */
  private lookupNative(startId: string, key: string): NativeLookup | undefined {
    const visited = new Set<string>();
    let current: string | null | undefined = startId;
    while (current !== null && current !== undefined) {
      if (visited.has(current)) return undefined;
      visited.add(current);
      const node = this.node(current);
      const prop = hasOwnProp(node.nativeProps, key) ? node.nativeProps![key] : undefined;
      if (prop) return { nodeId: current, key, prop };
      current = node.nativeProto;
    }
    return undefined;
  }

  private lookupOwnNative(id: string, key: string): NativeLookup | undefined {
    const node = this.node(id);
    const prop = hasOwnProp(node.nativeProps, key) ? node.nativeProps![key] : undefined;
    return prop ? { nodeId: id, key, prop } : undefined;
  }

  private readProperty(found: NativeLookup): unknown {
    const prop = found.prop;
    if (prop.kind === 'accessor') {
      return this.accessorMarker({
        xrayAccessor: true,
        ownerNode: found.nodeId,
        key: found.key,
      });
    }
    return this.wrapValue((prop as DataProperty).value);
  }

  private wrapValue(value: SlotValue): unknown {
    if (isRef(value)) return this.view(value.$ref);
    if (Array.isArray(value)) {
      // 嵌套字面量数组按值快照包裹，内部引用仍然是视图。
      return value.map((item) => this.wrapValue(item));
    }
    return value;
  }

  /** 供快照/诊断工具复用的值包裹入口。 */
  wrapPublic(value: SlotValue): unknown {
    return this.wrapValue(value);
  }

  private nativeProtoView(id: string): object | null {
    const nativeProto = this.node(id).nativeProto;
    if (nativeProto === null || nativeProto === undefined) return null;
    return this.view(nativeProto);
  }

  private slotIndex(id: string, key: string): number | undefined {
    const slots = this.node(id).arraySlots;
    if (!slots) return undefined;
    const numeric = Number(key);
    if (!Number.isInteger(numeric) || numeric < 0 || numeric >= slots.length) return undefined;
    return numeric;
  }

  private *iterateSlots(id: string): Generator<unknown> {
    const slots = this.node(id).arraySlots ?? [];
    for (const slot of slots) yield this.wrapValue(slot);
  }

  /** 取出视图背后的图节点 id，供诊断与快照使用。 */
  nodeIdOf(view: object): string | undefined {
    const holder = view as ViewHolder;
    const id = holder[NODE_ID];
    return id && this.graph.nodes[id] ? id : undefined;
  }

  /** 反查视图所属的 Xray 实例，供特权内置函数的 this 解析使用。 */
  static xrayOf(value: unknown): Xray | undefined {
    if (value === null || (typeof value !== 'object' && typeof value !== 'function')) return undefined;
    return XRAY_OWNERS.get(value as object);
  }

  /** 按图节点 id 反查所属 Xray 实例，供 call/apply/bind 元方法使用。 */
  static forNode(nodeId: string): Xray | undefined {
    return XRAY_BY_NODE.get(nodeId);
  }

  /** 原生自有属性描述（Xray 语义），内容遮蔽键返回 undefined。 */
  describe(view: object, key: string): PropertyDescriptor | undefined {
    const id = this.nodeIdOf(view);
    if (!id) return undefined;
    const found = this.lookupOwnNative(id, key);
    return found ? this.descriptorFor(found) : undefined;
  }

  /** 诊断：内容域在该对象上追加/遮蔽了哪些键。 */
  contentProperties(view: object): string[] {
    const id = this.nodeIdOf(view);
    if (!id) return [];
    return Object.keys(this.node(id).contentProps ?? {});
  }

  /** 诊断：该键是否被内容域追加或遮蔽。 */
  isContentShadowed(view: object, key: string): boolean {
    const id = this.nodeIdOf(view);
    if (!id) return false;
    return hasOwnProp(this.node(id).contentProps, key);
  }

  isView(value: unknown): value is object {
    if (value === null || (typeof value !== 'object' && typeof value !== 'function')) return false;
    return this.nodeIdOf(value) !== undefined;
  }
}
