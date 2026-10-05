export type Value = number | string | boolean;

export type CmpOp = "eq" | "ne" | "lt" | "le" | "gt" | "ge";

export type Predicate =
  | { kind: "cmp"; attr: string; cmp: CmpOp; value?: Value; attr2?: string }
  | { kind: "and"; args: Predicate[] }
  | { kind: "or"; args: Predicate[] }
  | { kind: "not"; arg: Predicate };

export type Query =
  | { op: "rel"; name: string }
  | { op: "select"; input: Query; predicate: Predicate }
  | { op: "project"; input: Query; attributes: string[] }
  | { op: "union"; left: Query; right: Query }
  | { op: "minus"; left: Query; right: Query };

export interface AttributeSpec {
  name: string;
  domain: Value[];
}

export interface RelationSpec {
  name: string;
  attributes: AttributeSpec[];
}

export interface Spec {
  relations: RelationSpec[];
  maxRows: number;
  queries: { Q1: Query; Q2: Query };
}

export interface RelInstance {
  attrs: string[];
  rows: Map<string, Value[]>;
}

export type Database = Map<string, RelInstance>;
