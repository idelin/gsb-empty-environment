import type { JsonValue } from './ast.ts';
import { deepEqual, isObject } from './ast.ts';
import type { Program } from './compiler.ts';
import { evaluate } from './interpreter.ts';
import type { EvalOptions, Match } from './interpreter.ts';
import { canonical } from './path.ts';
import type { Path } from './path.ts';
import { EditError, QueryError } from './errors.ts';

export type Edit = { op: 'set'; path: Path; value: JsonValue } | { op: 'delete'; path: Path };

/**
 * 版本化文档：每次 apply 产生新版本，旧版本根节点通过结构共享保留，
 * 因此旧游标在数组元素删除后仍能解析到旧版本中的原节点。
 * 所有 get() 返回的树按约定不可变（编辑永远走 apply）。
 */
export class VersionedDocument {
  private readonly versions = new Map<number, JsonValue>();
  current = 0;

  constructor(initial: JsonValue) {
    this.versions.set(0, initial);
  }

  get(version = this.current): JsonValue {
    const v = this.versions.get(version);
    if (v === undefined) throw new QueryError('NO_VERSION', `unknown document version ${version}`);
    return v;
  }

  apply(edits: Edit[]): number {
    let root = this.get();
    for (const edit of edits) {
      root = edit.op === 'set' ? setPath(root, edit.path, edit.value) : deletePath(root, edit.path);
    }
    this.current++;
    this.versions.set(this.current, root);
    return this.current;
  }
}

/** 稳定游标：路径 + 绑定版本。 */
export interface Cursor {
  path: Path;
  version: number;
}

export function cursorOf(match: Match, version: number): Cursor {
  return { path: match.path, version };
}

/** 在游标绑定的版本上解析；该版本中路径已不存在时返回 undefined。 */
export function resolveCursor(doc: VersionedDocument, cursor: Cursor): Match | undefined {
  let node = doc.get(cursor.version);
  for (const seg of cursor.path) {
    if (typeof seg === 'number') {
      if (!Array.isArray(node) || seg < 0 || seg >= node.length) return undefined;
      node = node[seg]!;
    } else {
      if (!isObject(node) || !Object.prototype.hasOwnProperty.call(node, seg)) return undefined;
      node = node[seg]!;
    }
  }
  return { path: cursor.path, value: node };
}

export interface ResultDiff {
  version: number;
  added: Match[];
  removed: Match[];
  changed: { path: Path; before: JsonValue; after: JsonValue }[];
}

/**
 * 增量结果集：跟踪某查询在文档最新版本上的结果，
 * 每次 sync 输出相对上一次的差量（added/removed/changed）。
 */
export class IncrementalResults {
  private current = new Map<string, Match>();
  private syncedVersion = -1;
  private readonly prog: Program;
  private readonly doc: VersionedDocument;
  private readonly opts: EvalOptions;

  constructor(prog: Program, doc: VersionedDocument, opts: EvalOptions = {}) {
    this.prog = prog;
    this.doc = doc;
    this.opts = opts;
  }

  get version(): number {
    return this.syncedVersion;
  }

  matches(): Match[] {
    return [...this.current.values()];
  }

  /** 在文档当前版本上重算并返回差量。 */
  sync(): ResultDiff {
    const version = this.doc.current;
    const next = new Map<string, Match>();
    for (const m of evaluate(this.doc.get(), this.prog, this.opts)) next.set(canonical(m.path), m);
    const added: Match[] = [];
    const removed: Match[] = [];
    const changed: { path: Path; before: JsonValue; after: JsonValue }[] = [];
    for (const [key, m] of next) {
      const prev = this.current.get(key);
      if (!prev) added.push(m);
      else if (!deepEqual(prev.value, m.value)) changed.push({ path: m.path, before: prev.value, after: m.value });
    }
    for (const [key, m] of this.current) if (!next.has(key)) removed.push(m);
    this.current = next;
    this.syncedVersion = version;
    return { version, added, removed, changed };
  }

  /** 当前结果的稳定游标（绑定到已同步版本）。 */
  cursors(): Cursor[] {
    return [...this.current.values()].map((m) => cursorOf(m, this.syncedVersion));
  }
}

// ---- 持久化（结构共享）编辑原语 ----

export function setPath(node: JsonValue, path: Path, value: JsonValue): JsonValue {
  if (path.length === 0) return value;
  const [head, ...rest] = path;
  if (typeof head === 'number') {
    if (head < 0) throw new EditError(`negative index ${head} is not editable`, path);
    if (node !== null && !Array.isArray(node)) throw new EditError('cannot index into non-array', path);
    const arr: JsonValue[] = Array.isArray(node) ? [...node] : [];
    if (head > arr.length) throw new EditError(`index ${head} beyond array length ${arr.length}`, path);
    if (head === arr.length && rest.length > 0) throw new EditError(`cannot create intermediate index ${head}`, path);
    arr[head] = setPath(head < arr.length ? arr[head]! : null, rest, value);
    return arr;
  }
  if (node !== null && !isObject(node)) throw new EditError('cannot key into non-object', path);
  const obj: Record<string, JsonValue> = isObject(node) ? { ...node } : {};
  obj[head] = setPath(Object.prototype.hasOwnProperty.call(obj, head) ? obj[head]! : null, rest, value);
  return obj;
}

export function deletePath(node: JsonValue, path: Path): JsonValue {
  if (path.length === 0) throw new EditError('cannot delete the document root', path);
  const [head, ...rest] = path;
  if (rest.length === 0) {
    if (typeof head === 'number') {
      if (!Array.isArray(node) || head < 0 || head >= node.length) {
        throw new EditError(`array index ${String(head)} does not exist`, path);
      }
      const arr = [...node];
      arr.splice(head, 1);
      return arr;
    }
    if (!isObject(node) || !Object.prototype.hasOwnProperty.call(node, head)) {
      throw new EditError(`key ${JSON.stringify(head)} does not exist`, path);
    }
    const obj = { ...node };
    delete obj[head];
    return obj;
  }
  if (typeof head === 'number') {
    if (!Array.isArray(node) || head < 0 || head >= node.length) throw new EditError('array index does not exist', path);
    const arr = [...node];
    arr[head] = deletePath(arr[head]!, rest);
    return arr;
  }
  if (!isObject(node) || !Object.prototype.hasOwnProperty.call(node, head)) {
    throw new EditError(`key ${JSON.stringify(head)} does not exist`, path);
  }
  const obj = { ...node };
  obj[head] = deletePath(obj[head]!, rest);
  return obj;
}
