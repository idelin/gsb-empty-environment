export type Value = number | string | boolean | null;

export interface VarVersion {
  name: string;
  version: number;
}

export type BinOp =
  | 'add' | 'sub' | 'mul' | 'div' | 'mod'
  | 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge'
  | 'and' | 'or';

export type Expr =
  | { op: 'lit'; value: Value }
  | { op: 'ref'; ref: VarVersion }
  | { op: 'neg' | 'not'; arg: Expr }
  | { op: BinOp; lhs: Expr; rhs: Expr };

interface EventBase {
  id: number;
  expr: Expr;
  value: Value;
  reads: VarVersion[];
  control: number[];
}

export interface WriteEvent extends EventBase {
  kind: 'write';
  target: VarVersion;
}

export interface CondEvent extends EventBase {
  kind: 'cond';
}

export interface AssertEvent extends EventBase {
  kind: 'assert';
  label: string;
  expected: Value;
  passed: boolean;
}

export type LogEvent = WriteEvent | CondEvent | AssertEvent;

export interface EventLog {
  events: LogEvent[];
}

export function varKey(v: VarVersion): string {
  return `${v.name}@${v.version}`;
}
