/**
 * 三层设置库：default < project < session（高层覆盖低层）。
 *
 * 语义：
 * - 显式 null 是一个“值”，会遮蔽低层，而不是删除。
 * - unset 只移除当前层的叶子，下层值自动浮现（继承）。
 * - 对象按叶子递归合并；数组与标量（含 null）整体替换。
 * - get/resolve 输出最终值及其来源层。
 * - transaction 内多路径更新统一提交、统一发布一次变更通知；异常则回滚。
 */

export type LayerName = 'default' | 'project' | 'session';
export const LAYERS: readonly LayerName[] = ['default', 'project', 'session'];

export type JsonObject = { [key: string]: JsonValue };
export type JsonValue = null | boolean | number | string | JsonValue[] | JsonObject;

export type Path = string | readonly string[];

export interface Change {
  layer: LayerName;
  path: string[];
  kind: 'set' | 'unset';
}

export type ChangeListener = (changes: readonly Change[]) => void;

export interface Resolution {
  /** 合并后的最终值；所有层都没有时为 undefined */
  value: JsonValue | undefined;
  /** 单一来源时为该层名；多叶子来自不同层时为 'merged'；未设置时为 undefined */
  source: LayerName | 'merged' | undefined;
  /** 每个叶子路径（点分，根为 ''）-> 来源层 */
  sources: Record<string, LayerName>;
}

export interface Transaction {
  set(layer: LayerName, path: Path, value: JsonValue): void;
  unset(layer: LayerName, path: Path): void;
}

const FORBIDDEN_KEYS = new Set(['__proto__', 'constructor', 'prototype']);

function isPlainObject(v: unknown): v is JsonObject {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function deepClone<T extends JsonValue>(v: T): T {
  if (Array.isArray(v)) return v.map((x) => deepClone(x)) as T;
  if (isPlainObject(v)) {
    const out: JsonObject = {};
    for (const [k, x] of Object.entries(v)) out[k] = deepClone(x);
    return out as T;
  }
  return v;
}

export function deepEqual(a: JsonValue | undefined, b: JsonValue | undefined): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((x, i) => deepEqual(x, b[i]));
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const ka = Object.keys(a);
    const kb = Object.keys(b);
    return ka.length === kb.length && ka.every((k) => k in b && deepEqual(a[k], b[k]));
  }
  return false;
}

export function normalizePath(path: Path): string[] {
  const keys = typeof path === 'string' ? (path === '' ? [] : path.split('.')) : [...path];
  for (const k of keys) {
    if (k === '' || FORBIDDEN_KEYS.has(k)) throw new Error(`invalid path key: ${JSON.stringify(k)}`);
  }
  return keys;
}

function dotted(keys: readonly string[]): string {
  return keys.join('.');
}

function getAt(root: JsonObject, keys: readonly string[]): { found: boolean; value?: JsonValue } {
  let node: JsonValue = root;
  for (const k of keys) {
    if (!isPlainObject(node) || !(k in node)) return { found: false };
    node = node[k];
  }
  return { found: true, value: node };
}

/** 把 value 写入 layer 树：对象按叶合并，数组/标量/null 整体替换。 */
function mergeIntoLayer(tree: JsonObject, keys: readonly string[], value: JsonValue): void {
  if (keys.length === 0) {
    if (!isPlainObject(value)) throw new Error('root value must be an object');
    for (const [k, v] of Object.entries(value)) mergeIntoLayer(tree, [k], v);
    return;
  }
  const [head, ...rest] = keys;
  if (rest.length === 0) {
    if (isPlainObject(value) && isPlainObject(tree[head])) {
      for (const [k, v] of Object.entries(value)) mergeIntoLayer(tree[head] as JsonObject, [k], v);
    } else if (isPlainObject(value)) {
      const obj: JsonObject = {};
      for (const [k, v] of Object.entries(value)) mergeIntoLayer(obj, [k], v);
      tree[head] = obj;
    } else {
      tree[head] = deepClone(value);
    }
    return;
  }
  let child = tree[head];
  if (!isPlainObject(child)) {
    child = {};
    tree[head] = child;
  }
  mergeIntoLayer(child, rest, value);
}

/** 只移除当前层的叶子，并清理变空的祖先对象。返回是否有实际删除。 */
function unsetInLayer(tree: JsonObject, keys: readonly string[]): boolean {
  if (keys.length === 0) {
    const had = Object.keys(tree).length > 0;
    for (const k of Object.keys(tree)) delete tree[k];
    return had;
  }
  const [head, ...rest] = keys;
  if (rest.length === 0) {
    if (!(head in tree)) return false;
    delete tree[head];
    return true;
  }
  const child = tree[head];
  if (!isPlainObject(child)) return false;
  const removed = unsetInLayer(child, rest);
  if (removed && Object.keys(child).length === 0) delete tree[head];
  return removed;
}

function clearSources(sources: Record<string, LayerName>, prefix: readonly string[]): void {
  const p = dotted(prefix);
  for (const key of Object.keys(sources)) {
    if (p === '' || key === p || key.startsWith(p + '.')) delete sources[key];
  }
}

function recordSources(value: JsonValue, layer: LayerName, prefix: readonly string[], sources: Record<string, LayerName>): void {
  if (isPlainObject(value)) {
    for (const [k, v] of Object.entries(value)) recordSources(v, layer, [...prefix, k], sources);
    return;
  }
  sources[dotted(prefix)] = layer;
}

/** 高层 overlay 合并到已解析的 base 上，并更新叶子来源。 */
function mergeOverlay(
  base: JsonObject,
  overlay: JsonObject,
  layer: LayerName,
  prefix: readonly string[],
  sources: Record<string, LayerName>,
): JsonObject {
  const out: JsonObject = { ...base };
  for (const [k, v] of Object.entries(overlay)) {
    const childPrefix = [...prefix, k];
    const bv = out[k];
    if (isPlainObject(bv) && isPlainObject(v)) {
      out[k] = mergeOverlay(bv, v, layer, childPrefix, sources);
    } else {
      clearSources(sources, childPrefix);
      recordSources(v, layer, childPrefix, sources);
      out[k] = deepClone(v);
    }
  }
  return out;
}

export class SettingsStore {
  private layers: Record<LayerName, JsonObject> = { default: {}, project: {}, session: {} };
  private listeners = new Set<ChangeListener>();
  private txDepth = 0;
  private pending: Change[] = [];

  private readonly txHandle: Transaction = {
    set: (layer, path, value) => this.set(layer, path, value),
    unset: (layer, path) => this.unset(layer, path),
  };

  set(layer: LayerName, path: Path, value: JsonValue): void {
    const keys = normalizePath(path);
    const before = getAt(this.layers[layer], keys);
    mergeIntoLayer(this.layers[layer], keys, value);
    const after = getAt(this.layers[layer], keys);
    if (!before.found || !after.found || !deepEqual(before.value, after.value)) {
      this.emit({ layer, path: keys, kind: 'set' });
    }
  }

  unset(layer: LayerName, path: Path): void {
    const keys = normalizePath(path);
    if (unsetInLayer(this.layers[layer], keys)) {
      this.emit({ layer, path: keys, kind: 'unset' });
    }
  }

  /** 事务：多路径更新统一提交；监听器只收到一次批量通知；抛异常则整体回滚且不发布。 */
  transaction(fn: (tx: Transaction) => void): void {
    const snapshot: Record<LayerName, JsonObject> = {
      default: deepClone(this.layers.default),
      project: deepClone(this.layers.project),
      session: deepClone(this.layers.session),
    };
    const mark = this.pending.length;
    this.txDepth++;
    let ok = false;
    try {
      fn(this.txHandle);
      ok = true;
    } finally {
      this.txDepth--;
      if (!ok) {
        this.layers = snapshot;
        this.pending.length = mark;
      }
      if (this.txDepth === 0) this.flush();
    }
  }

  subscribe(listener: ChangeListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** 最终合并值；所有层都没有时为 undefined。 */
  get(path: Path): JsonValue | undefined {
    return this.resolve(path).value;
  }

  /** 最终值 + 来源。 */
  resolve(path: Path): Resolution {
    const keys = normalizePath(path);
    // 先在根把三层折叠成一棵树（高层标量/数组/null 会整体遮蔽低层子树），
    // 再定位到所查路径，避免深层路径绕过高层遮蔽从低层漏出。
    const rootSources: Record<string, LayerName> = {};
    let tree: JsonValue = {};
    for (const layer of LAYERS) {
      const layerTree = this.layers[layer];
      if (Object.keys(layerTree).length === 0) continue;
      tree = mergeOverlay(tree as JsonObject, layerTree, layer, [], rootSources);
    }
    let node: JsonValue = tree;
    for (const k of keys) {
      if (!isPlainObject(node) || !(k in node)) {
        return { value: undefined, source: undefined, sources: {} };
      }
      node = node[k];
    }
    const prefix = dotted(keys);
    const sources: Record<string, LayerName> = {};
    for (const [p, layer] of Object.entries(rootSources)) {
      if (prefix === '' || p === prefix || p.startsWith(prefix + '.')) sources[p] = layer;
    }
    let lastLayer: LayerName | undefined;
    for (const layer of LAYERS) {
      if (getAt(this.layers[layer], keys).found) lastLayer = layer;
    }
    const distinct = new Set(Object.values(sources));
    const source: Resolution['source'] =
      distinct.size === 0 ? lastLayer : distinct.size === 1 ? [...distinct][0] : 'merged';
    return { value: deepClone(node), source, sources };
  }

  /** 调试用：各层原始树的深拷贝。 */
  snapshotLayers(): Record<LayerName, JsonObject> {
    return {
      default: deepClone(this.layers.default),
      project: deepClone(this.layers.project),
      session: deepClone(this.layers.session),
    };
  }

  private emit(change: Change): void {
    this.pending.push(change);
    if (this.txDepth === 0) this.flush();
  }

  private flush(): void {
    if (this.pending.length === 0 || this.listeners.size === 0) {
      this.pending.length = 0;
      return;
    }
    const seen = new Set<string>();
    const batch: Change[] = [];
    for (const c of this.pending) {
      const id = `${c.layer}${c.kind}${dotted(c.path)}`;
      if (!seen.has(id)) {
        seen.add(id);
        batch.push(c);
      }
    }
    this.pending.length = 0;
    for (const l of this.listeners) l(batch);
  }
}
