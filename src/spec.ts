/**
 * 规约（Spec）：层级状态 + 事件表 + 守卫 + 原子多字段动作 + 不变量。
 * 执行器与模型检查器均从同一份规约生成，但各自独立解释一步语义。
 */

export type Scalar = number | string | boolean;
export type Data = Record<string, Scalar>;

/** 事件：名称 + 负载 + 可选目标节点（用于回放时校验节点树）。 */
export interface Ev {
  type: string;
  payload?: Data;
  target?: string;
}

/** 层级状态声明。parent 为空为根；initial 指向默认子状态。 */
export interface StateDecl {
  id: string;
  parent?: string;
  initial?: string;
}

/** 迁移：from 命中活动路径中任意祖先；动作返回部分字段补丁，原子应用。 */
export interface Transition {
  event: string;
  from?: string;
  guard?: (data: Data, payload: Data) => boolean;
  action?: (data: Data, payload: Data) => Data;
  to?: string;
  effects?: string[];
}

export interface Invariant {
  name: string;
  check: (data: Data, path: string[]) => boolean;
}

export interface Spec {
  name: string;
  version: number;
  states: StateDecl[];
  initialData: () => Data;
  transitions: Transition[];
  invariants: Invariant[];
  /** 数据字段的有限域（模型检查用）。 */
  domains?: Record<string, Scalar[]>;
  /** 每个事件的有限负载域（模型检查用）。 */
  eventDomains?: Record<string, Data[]>;
}

/** 完整配置 = 活动状态路径（根→叶）+ 数据。 */
export interface Config {
  path: string[];
  data: Data;
}

export interface CompiledSpec {
  spec: Spec;
  byId: Map<string, StateDecl>;
  children: Map<string, StateDecl[]>;
  byEvent: Map<string, Transition[]>;
  initialPath: string[];
}

export function compile(spec: Spec): CompiledSpec {
  const byId = new Map<string, StateDecl>();
  const children = new Map<string, StateDecl[]>();
  for (const s of spec.states) {
    if (byId.has(s.id)) throw new Error(`duplicate state id: ${s.id}`);
    byId.set(s.id, s);
  }
  for (const s of spec.states) {
    if (s.parent !== undefined) {
      if (!byId.has(s.parent)) throw new Error(`unknown parent: ${s.parent}`);
      const list = children.get(s.parent) ?? [];
      list.push(s);
      children.set(s.parent, list);
    }
  }
  for (const s of spec.states) {
    if (s.initial !== undefined) {
      const child = byId.get(s.initial);
      if (!child || child.parent !== s.id) {
        throw new Error(`state ${s.id} has invalid initial child ${s.initial}`);
      }
    }
  }
  const roots = spec.states.filter((s) => s.parent === undefined);
  if (roots.length !== 1) throw new Error(`spec must have exactly one root, got ${roots.length}`);

  const initialPath: string[] = [];
  let cursor: StateDecl | undefined = roots[0];
  while (cursor) {
    initialPath.push(cursor.id);
    const nextId = cursor.initial;
    if (nextId === undefined) break;
    cursor = byId.get(nextId);
  }

  const byEvent = new Map<string, Transition[]>();
  for (const t of spec.transitions) {
    const list = byEvent.get(t.event) ?? [];
    list.push(t);
    byEvent.set(t.event, list);
  }
  return { spec, byId, children, byEvent, initialPath };
}

export function initialConfig(compiled: CompiledSpec): Config {
  return { path: [...compiled.initialPath], data: compiled.spec.initialData() };
}

/** 进入目标状态：目标路径 = 根→目标的祖先链 + 目标自身沿 initial 下沉。 */
export function enterPath(compiled: CompiledSpec, targetId: string): string[] {
  const target = compiled.byId.get(targetId);
  if (!target) throw new Error(`unknown target state: ${targetId}`);
  const ancestors: string[] = [];
  let cursor: StateDecl | undefined = target;
  while (cursor) {
    ancestors.unshift(cursor.id);
    cursor = cursor.parent === undefined ? undefined : compiled.byId.get(cursor.parent);
  }
  const path = [...ancestors];
  let leaf: StateDecl | undefined = target;
  while (leaf && leaf.initial !== undefined) {
    leaf = compiled.byId.get(leaf.initial);
    if (leaf) path.push(leaf.id);
  }
  return path;
}

/** 校验一条路径在该规约下是否结构合法（热换迁移证据用）。 */
export function validatePath(compiled: CompiledSpec, path: string[]): string[] {
  const errors: string[] = [];
  if (path.length === 0) return ["path is empty"];
  for (let i = 0; i < path.length; i++) {
    const id = path[i]!;
    const decl = compiled.byId.get(id);
    if (!decl) {
      errors.push(`unknown state: ${id}`);
      continue;
    }
    if (i === 0) {
      if (decl.parent !== undefined) errors.push(`path root ${id} is not a root state`);
    } else if (decl.parent !== path[i - 1]) {
      errors.push(`state ${id} is not a child of ${path[i - 1]}`);
    }
  }
  return errors;
}

/** 稳定序列化（键排序），保证相同状态得到相同字符串。 */
export function stableStringify(value: unknown): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value) ?? "undefined";
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(",")}]`;
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${stableStringify(obj[k])}`).join(",")}}`;
}

export function configEquals(a: Config, b: Config): boolean {
  return stableStringify(a) === stableStringify(b);
}
