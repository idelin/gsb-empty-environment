import type { ContentAccessorProperty, ContentProperty, GraphNode, ObjectGraph, SlotValue } from '../src/graph.ts';
import { isRef } from '../src/graph.ts';

/**
 * 内容域语义的最小模拟：它按内容域规则解析属性（含遮蔽、追加 getter、
 * 替换后的原型）。用于对照测试——证明同一份图在 Xray 视图下不会触发
 * 这里的内容函数，而内容侧访问确实会触发。
 */
export class ContentMirror {
  private readonly proxies = new Map<string, object>();
  private readonly graph: ObjectGraph;

  constructor(graph: ObjectGraph) {
    this.graph = graph;
  }

  mirror(id: string): object {
    const cached = this.proxies.get(id);
    if (cached) return cached;

    const node = this.graph.nodes[id];
    const target = node.intrinsic === 'Function' ? (() => {}) : {};
    const proxy = new Proxy(target, {
      get: (_, key, receiver) => {
        if (typeof key !== 'string') return Reflect.get(target, key, receiver);
        const found = this.lookup(id, key);
        if (!found) return undefined;
        return this.read(found.owner, found.prop);
      },
      has: (_, key) => typeof key === 'string' && this.lookup(id, key) !== undefined,
      ownKeys: () => {
        const keys = new Set<string>();
        for (const current of this.chain(id)) {
          const currentNode = this.graph.nodes[current];
          for (const key of Object.keys(currentNode.contentProps ?? {})) keys.add(key);
          for (const key of Object.keys(currentNode.nativeProps ?? {})) keys.add(key);
        }
        return [...keys];
      },
      getPrototypeOf: () => {
        const pointer = this.graph.nodes[id].contentProto ?? this.graph.nodes[id].nativeProto;
        return pointer ? this.mirror(pointer) : null;
      },
      apply: () => {
        throw new Error('content functions are not modeled as callable in this harness');
      },
    }) as unknown as object;
    this.proxies.set(id, proxy);
    return proxy;
  }

  private chain(startId: string): string[] {
    const result: string[] = [];
    const visited = new Set<string>();
    let current: string | null | undefined = startId;
    while (current !== null && current !== undefined && !visited.has(current)) {
      visited.add(current);
      result.push(current);
      const node = this.graph.nodes[current];
      current = node.contentProto ?? node.nativeProto;
    }
    return result;
  }

  private lookup(id: string, key: string): { owner: string; prop: ContentProperty } | undefined {
    for (const current of this.chain(id)) {
      const node = this.graph.nodes[current];
      if (node.contentProps && Object.prototype.hasOwnProperty.call(node.contentProps, key)) {
        return { owner: current, prop: node.contentProps[key] };
      }
      if (node.nativeProps && Object.prototype.hasOwnProperty.call(node.nativeProps, key)) {
        return { owner: current, prop: node.nativeProps[key] };
      }
    }
    return undefined;
  }

  private read(ownerId: string, prop: ContentProperty): unknown {
    if (prop.kind === 'accessor') {
      const accessor = prop as ContentAccessorProperty;
      if (accessor.get) return accessor.get();
      return undefined;
    }
    return this.wrap(prop.value, ownerId);
  }

  private wrap(value: SlotValue, ownerId: string): unknown {
    if (isRef(value)) return this.mirror(value.$ref);
    if (Array.isArray(value)) return value.map((item) => this.wrap(item, ownerId));
    return value;
  }
}
