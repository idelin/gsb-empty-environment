/** JSON 兼容值：查询输入树的节点类型。 */
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type CmpOp = '==' | '!=' | '<' | '<=' | '>' | '>=';

/** 谓词表达式（编译为闭包，绝不经过宿主 eval）。 */
export type Expr =
  | { kind: 'lit'; value: JsonValue }
  | { kind: 'cur'; path: PathSegment[] } // @ 相对路径
  | { kind: 'root'; path: PathSegment[] } // $ 绝对路径（需要全局信息）
  | { kind: 'cmp'; op: CmpOp; left: Expr; right: Expr }
  | { kind: 'and'; left: Expr; right: Expr }
  | { kind: 'or'; left: Expr; right: Expr }
  | { kind: 'not'; expr: Expr }
  | { kind: 'exists'; expr: Expr };

/** 路径段（查询计划的一个阶段）。 */
export type PathSegment =
  | { kind: 'child'; key: string }
  | { kind: 'index'; index: number }
  | { kind: 'slice'; start: number | null; end: number | null; step: number }
  | { kind: 'wildcard' }
  | { kind: 'recursive' }
  | { kind: 'filter'; expr: Expr };

export interface Query {
  source: string;
  segments: PathSegment[];
}

export function isObject(v: JsonValue): v is { [key: string]: JsonValue } {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

export function isContainer(v: JsonValue): v is JsonValue[] | { [key: string]: JsonValue } {
  return typeof v === 'object' && v !== null;
}

/** 深比较（对象键无序：按键集合比较，与顺序无关）。 */
export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a)) {
    if (!Array.isArray(b) || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (!deepEqual(a[i]!, b[i]!)) return false;
    return true;
  }
  if (Array.isArray(b)) return false;
  if (typeof a === 'object') {
    const ka = Object.keys(a);
    const kb = Object.keys(b as object);
    if (ka.length !== kb.length) return false;
    for (const k of ka) {
      if (!Object.prototype.hasOwnProperty.call(b, k)) return false;
      if (!deepEqual((a as Record<string, JsonValue>)[k]!, (b as Record<string, JsonValue>)[k]!)) return false;
    }
    return true;
  }
  return false;
}
