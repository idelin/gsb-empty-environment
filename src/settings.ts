/**
 * 三层设置库：default（默认）< project（项目）< session（会话），高层优先。
 *
 * 语义约定：
 * - 显式 null 是合法值，与“未设置”严格区分；null 作为叶子会遮蔽下层的整个子树。
 * - unset 只撤销当前层的覆盖，下层（优先级更低）的值重新显现。
 * - 对象按叶合并：各层同一对象路径下的叶子逐键取最高层；数组是叶子，整体替换。
 * - 事务更新（update / transaction）把多条路径的修改作为一个批次，统一发布一次通知。
 */

export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

export const LAYERS = ['default', 'project', 'session'] as const;
export type LayerName = (typeof LAYERS)[number];

/** 点分字符串（"editor.font.size"）或段数组（["editor","font","size"]）。 */
export type Path = string | readonly string[];

/** 解析结果中单个叶子的来源信息。 */
export interface ResolvedLeaf {
  readonly path: readonly string[];
  readonly value: JsonValue;
  readonly source: LayerName;
}

export interface Resolution {
  readonly path: readonly string[];
  /** 合并后的最终值；所有层都未定义时为 undefined。 */
  readonly value: JsonValue | undefined;
  /**
   * 唯一来源层：当最终值（叶子或整棵子树）全部来自同一层时给出该层；
   * 对象由多层叶子合并而成时为 undefined，逐叶来源见 leaves。
   */
  readonly source: LayerName | undefined;
  /** 逐叶来源（叶子含 null 与数组）；未定义时为空数组。 */
  readonly leaves: readonly ResolvedLeaf[];
}

export type Mutation =
  | { readonly layer: LayerName; readonly path: Path; readonly op: 'set'; readonly value: JsonValue }
  | { readonly layer: LayerName; readonly path: Path; readonly op: 'unset' };

export interface ChangeEvent {
  /** 本批次应用的全部变更（事务统一发布时包含多条）。 */
  readonly changes: readonly Mutation[];
  /** 批次应用后的根节点解析结果（最终值 + 来源）。 */
  readonly resolution: Resolution;
}

export type ChangeListener = (event: ChangeEvent) => void;

export interface Transaction {
  set(layer: LayerName, path: Path, value: JsonValue): void;
  unset(layer: LayerName, path: Path): void;
}

const FORBIDDEN_KEYS = new Set(['__proto__', 'constructor', 'prototype']);

function isPlainObject(value: unknown): value is JsonObject {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function normalizePath(path: Path): readonly string[] {
  const segments = typeof path === 'string' ? (path === '' ? [] : path.split('.')) : [...path];
  for (const segment of segments) {
    if (segment === '') throw new Error(`路径包含空段: ${JSON.stringify(path)}`);
    if (FORBIDDEN_KEYS.has(segment)) throw new Error(`路径包含非法键: ${segment}`);
  }
  return segments;
}

function validateValue(value: JsonValue, where: string): void {
  if (value === null) return;
  switch (typeof value) {
    case 'string':
    case 'boolean':
      return;
    case 'number':
      if (Number.isNaN(value)) throw new Error(`非法值 NaN: ${where}`);
      return;
    case 'object':
      if (Array.isArray(value)) {
        value.forEach((item, index) => validateValue(item, `${where}[${index}]`));
        return;
      }
      if (!isPlainObject(value)) throw new Error(`仅支持 JSON 风格 plain object: ${where}`);
      for (const key of Object.keys(value)) {
        if (FORBIDDEN_KEYS.has(key)) throw new Error(`值中包含非法键 ${key}: ${where}`);
        validateValue(value[key], `${where}.${key}`);
      }
      return;
    default:
      throw new Error(`不支持的值类型 ${typeof value}: ${where}（删除请用 unset，显式空值请用 null）`);
  }
}

function cloneValue<T extends JsonValue>(value: T): T {
  if (Array.isArray(value)) return value.map((item) => cloneValue(item)) as T;
  if (isPlainObject(value)) {
    const out: JsonObject = {};
    for (const key of Object.keys(value)) out[key] = cloneValue(value[key]);
    return out as T;
  }
  return value;
}

function setPath(tree: JsonObject, segments: readonly string[], value: JsonValue): void {
  if (segments.length === 0) {
    if (!isPlainObject(value)) throw new Error('根路径只能 set 对象值');
    for (const key of Object.keys(tree)) delete tree[key];
    Object.assign(tree, value);
    return;
  }
  let node = tree;
  for (const segment of segments.slice(0, -1)) {
    const next = node[segment];
    if (isPlainObject(next)) {
      node = next;
    } else {
      const created: JsonObject = {};
      node[segment] = created;
      node = created;
    }
  }
  node[segments[segments.length - 1]] = value;
}

function unsetPath(tree: JsonObject, segments: readonly string[]): void {
  if (segments.length === 0) {
    for (const key of Object.keys(tree)) delete tree[key];
    return;
  }
  const stack: Array<{ node: JsonObject; key: string }> = [];
  let node: JsonValue = tree;
  for (const segment of segments) {
    if (!isPlainObject(node)) return;
    stack.push({ node, key: segment });
    node = node[segment];
  }
  const last = stack[stack.length - 1];
  if (!isPlainObject(last.node) || !(last.key in last.node)) return;
  delete last.node[last.key];
  // 自底向上清理因 unset 变空的父对象，保持层内树整洁。
  for (let index = stack.length - 2; index >= 0; index -= 1) {
    const { node: parent, key } = stack[index];
    const child = parent[key];
    if (isPlainObject(child) && Object.keys(child).length === 0) delete parent[key];
    else break;
  }
}

interface LayerNode {
  readonly layer: LayerName;
  readonly node: JsonValue;
}

interface Merged {
  readonly value: JsonValue;
  readonly leaves: ResolvedLeaf[];
  /** 对象节点的子树（按键）；叶子节点为 undefined。 */
  readonly children?: ReadonlyMap<string, Merged>;
}

/** 把单层的子树包装成 Merged：叶子（含 null、数组）记录来源，对象递归展开。 */
function wrapNode(layer: LayerName, node: JsonValue, basePath: readonly string[]): Merged {
  if (!isPlainObject(node)) {
    const value = cloneValue(node);
    return { value, leaves: [{ path: basePath, value: cloneValue(node), source: layer }] };
  }
  const out: JsonObject = {};
  const leaves: ResolvedLeaf[] = [];
  const children = new Map<string, Merged>();
  for (const key of Object.keys(node)) {
    const child = wrapNode(layer, node[key], [...basePath, key]);
    children.set(key, child);
    out[key] = child.value;
    leaves.push(...child.leaves);
  }
  return { value: out, leaves, children };
}

/**
 *  pairwise 合并（low 在下、high 在上）：
 *  - 高层是叶子（含 null、数组）→ 高层整体遮蔽下层；
 *  - 高层是对象、下层是叶子 → 高层对象遮蔽下层叶子（下层整棵子树不再重现）；
 *  - 两边都是对象 → 按键递归合并（按叶合并）。
 */
function mergePair(low: Merged | undefined, high: Merged): Merged {
  if (low === undefined) return high;
  if (high.children === undefined || low.children === undefined) return high;
  const out: JsonObject = {};
  const leaves: ResolvedLeaf[] = [];
  const children = new Map<string, Merged>();
  const keys = new Set<string>([...low.children.keys(), ...high.children.keys()]);
  for (const key of keys) {
    const highChild = high.children.get(key);
    const merged = highChild === undefined ? low.children.get(key)! : mergePair(low.children.get(key), highChild);
    children.set(key, merged);
    out[key] = merged.value;
    leaves.push(...merged.leaves);
  }
  return { value: out, leaves, children };
}

/** 自低向高逐层折叠合并。 */
function mergeEntries(entries: readonly LayerNode[], basePath: readonly string[]): Merged {
  let acc: Merged | undefined;
  for (const entry of entries) acc = mergePair(acc, wrapNode(entry.layer, entry.node, basePath));
  return acc!;
}

export class SettingsStore {
  private readonly layers: Record<LayerName, JsonObject> = { default: {}, project: {}, session: {} };
  private readonly listeners = new Set<ChangeListener>();

  constructor(initial?: Partial<Record<LayerName, JsonObject>>) {
    if (!initial) return;
    const mutations: Mutation[] = [];
    for (const layer of LAYERS) {
      const tree = initial[layer];
      if (tree !== undefined) mutations.push({ layer, path: [], op: 'set', value: tree });
    }
    this.update(mutations);
  }

  /** 最终合并值；path 省略时返回整棵合并树。返回值是深拷贝，可安全修改。 */
  get(path: Path = []): JsonValue | undefined {
    return this.resolve(path).value;
  }

  /** 最终值 + 来源（唯一来源层 / 逐叶来源）。 */
  resolve(path: Path = []): Resolution {
    const segments = normalizePath(path);
    const empty: Resolution = { path: segments, value: undefined, source: undefined, leaves: [] };
    // 沿查询路径逐段下降：若某一前缀的合并结果是叶子（最高可见层为叶子），
    // 则更深的路径整体被遮蔽，直接判为未定义；被遮蔽的下层叶子不再参与。
    let cursors: LayerNode[] = LAYERS.map((layer) => ({ layer, node: this.layers[layer] }));
    for (const segment of segments) {
      const visible = cursors.filter((cursor) => cursor.node !== undefined);
      if (visible.length === 0) return empty;
      const top = visible[visible.length - 1];
      if (!isPlainObject(top.node)) return empty;
      // 仅保留自最高层向下连续的对象层：一旦遇到叶子，该叶子及其下层的整棵子树
      // 在此前缀处均已被遮蔽（与 mergeEntries 的折叠语义一致），不得再向下参与。
      const run: LayerNode[] = [];
      for (let index = visible.length - 1; index >= 0; index -= 1) {
        if (!isPlainObject(visible[index].node)) break;
        run.push(visible[index]);
      }
      const next: LayerNode[] = [];
      for (let index = run.length - 1; index >= 0; index -= 1) {
        const cursor = run[index];
        const node = cursor.node as JsonObject;
        if (segment in node) next.push({ layer: cursor.layer, node: node[segment] });
      }
      cursors = next;
    }
    const entries = cursors.filter((cursor) => cursor.node !== undefined);
    if (entries.length === 0) return empty;
    const { value, leaves } = mergeEntries(entries, segments);
    const first = leaves.length > 0 ? leaves[0].source : undefined;
    const source = first !== undefined && leaves.every((leaf) => leaf.source === first) ? first : undefined;
    return { path: segments, value, source, leaves };
  }

  /** 便捷方法：唯一来源层（多层合并的对象返回 undefined）。 */
  sourceOf(path: Path): LayerName | undefined {
    return this.resolve(path).source;
  }

  set(layer: LayerName, path: Path, value: JsonValue): void {
    this.update([{ layer, path, op: 'set', value }]);
  }

  /** 只撤销指定层的覆盖；下层值（若有）重新生效。 */
  unset(layer: LayerName, path: Path): void {
    this.update([{ layer, path, op: 'unset' }]);
  }

  /** 事务更新：整批应用后统一发布一次通知。 */
  update(mutations: readonly Mutation[]): void {
    if (mutations.length === 0) return;
    const applied: Mutation[] = [];
    for (const mutation of mutations) applied.push(this.apply(mutation));
    this.publish(applied);
  }

  /** 事务的回调形式：tx 上累积的修改在回调返回后一次性应用并发布。 */
  transaction(fn: (tx: Transaction) => void): void {
    const batch: Mutation[] = [];
    fn({
      set: (layer, path, value) => {
        batch.push({ layer, path, op: 'set', value });
      },
      unset: (layer, path) => {
        batch.push({ layer, path, op: 'unset' });
      },
    });
    this.update(batch);
  }

  /** 订阅变更；每个事务批次只回调一次。返回退订函数。 */
  subscribe(listener: ChangeListener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  /** 各层原始覆盖树的深拷贝（未经合并），用于核对与持久化。 */
  layerSnapshot(): Record<LayerName, JsonObject> {
    return {
      default: cloneValue(this.layers.default),
      project: cloneValue(this.layers.project),
      session: cloneValue(this.layers.session),
    };
  }

  private apply(mutation: Mutation): Mutation {
    const segments = normalizePath(mutation.path);
    if (mutation.op === 'set') {
      validateValue(mutation.value, `set(${mutation.layer}, ${segments.join('.') || '<root>'})`);
      const stored = cloneValue(mutation.value);
      setPath(this.layers[mutation.layer], segments, stored);
      return { ...mutation, path: segments, value: stored };
    }
    unsetPath(this.layers[mutation.layer], segments);
    return { ...mutation, path: segments };
  }

  private publish(changes: readonly Mutation[]): void {
    if (this.listeners.size === 0) return;
    const event: ChangeEvent = { changes, resolution: this.resolve([]) };
    for (const listener of this.listeners) listener(event);
  }
}
