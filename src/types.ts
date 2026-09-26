export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

export type Condition =
  | { op: 'present'; field: string }
  | { op: 'absent'; field: string }
  | { op: 'equals'; field: string; value: Json }
  | { op: 'and'; all: Condition[] }
  | { op: 'or'; any: Condition[] }
  | { op: 'not'; cond: Condition };

export type GroupKind = 'exactlyOne' | 'atLeastOne' | 'atMost';

export interface Group {
  kind: GroupKind;
  fields: string[];
  k?: number;
  when?: Condition;
  name?: string;
}

export type UndecidedPolicy = 'disabled' | 'enabled' | 'error';

export interface Schema {
  groups?: Group[];
  onUndecidedCondition?: UndecidedPolicy;
}
