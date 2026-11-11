import type { ParamType } from './ast.ts';

export interface CParam {
  kind: 'param';
  slot: number;
}

export interface CLit {
  kind: 'lit';
  value: number | boolean | null;
}

export interface CCol {
  kind: 'col';
  qualifier: string | null;
  name: string;
}

export interface CBool {
  kind: 'bool';
  value: boolean;
}

export interface CBin {
  kind: 'bin';
  op: '=' | '<>' | '<' | '>' | '<=' | '>=' | 'LIKE' | 'AND' | 'OR';
  left: CExpr;
  right: CExpr;
}

export interface CUn {
  kind: 'un';
  op: 'NOT' | '-';
  expr: CExpr;
}

export interface CIsNull {
  kind: 'isNull';
  expr: CExpr;
}

export interface CInList {
  kind: 'inList';
  expr: CExpr;
  slots: number[];
  negated: boolean;
}

export interface CInQuery {
  kind: 'inQuery';
  expr: CExpr;
  query: CSelect;
  negated: boolean;
}

export interface CExists {
  kind: 'exists';
  query: CSelect;
}

export interface CScalar {
  kind: 'scalarQuery';
  query: CSelect;
}

export interface CFunc {
  kind: 'func';
  name: string;
  args: CExpr[];
  star: boolean;
  distinct: boolean;
}

export interface CBetween {
  kind: 'between';
  expr: CExpr;
  low: CExpr;
  high: CExpr;
  negated: boolean;
}

export type CExpr =
  | CParam
  | CLit
  | CCol
  | CBool
  | CBin
  | CUn
  | CIsNull
  | CInList
  | CInQuery
  | CExists
  | CScalar
  | CFunc
  | CBetween;

export interface CStarItem {
  star: true;
  qualifier: string | null;
}

export interface CExprItem {
  star: false;
  expr: CExpr;
  alias: string | null;
}

export type CSelectItem = CStarItem | CExprItem;

export interface CTable {
  kind: 'table';
  schema: string | null;
  name: string;
  alias: string | null;
}

export interface CSub {
  kind: 'subquery';
  query: CSelect;
  alias: string;
}

export type CRelation = CTable | CSub;

export interface CJoin {
  type: 'inner' | 'left' | 'right' | 'full';
  relation: CRelation;
  on: CExpr;
}

export interface COrder {
  expr: CExpr;
  direction: 'asc' | 'desc';
}

export interface CSelect {
  kind: 'select';
  distinct: boolean;
  selectList: CSelectItem[];
  from: CRelation;
  joins: CJoin[];
  where: CExpr | null;
  groupBy: CExpr[];
  having: CExpr | null;
  orderBy: COrder[];
  limit: number | null;
}

export interface ParamSlot {
  name: string;
  paramType: ParamType;
}

export interface CanonicalQuery {
  tree: CSelect;
  slots: ParamSlot[];
}

export function equalCanonical(a: unknown, b: unknown): boolean {
  if (a === b) {
    return true;
  }
  if (typeof a !== typeof b || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) {
      return false;
    }
    return a.every((value, index) => equalCanonical(value, b[index]));
  }
  if (typeof a !== 'object' || typeof b !== 'object') {
    return false;
  }
  const left = a as Record<string, unknown>;
  const right = b as Record<string, unknown>;
  const leftKeys = Object.keys(left);
  const rightKeys = Object.keys(right);
  if (leftKeys.length !== rightKeys.length) {
    return false;
  }
  return leftKeys.every((key) => equalCanonical(left[key], right[key]));
}
