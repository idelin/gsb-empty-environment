export interface VarVersion {
  name: string;
  version: number;
}

export type Value = number | boolean | string | null;

export type EventKind = 'const' | 'op' | 'branch' | 'assert';

export interface LogEvent {
  id: number;
  kind: EventKind;
  writes: VarVersion | null;
  reads: VarVersion[];
  control: number[];
  op: string | null;
  value: Value;
}

export interface EventLog {
  events: LogEvent[];
}

export type GapKind = 'missing-producer' | 'missing-control';

export interface Gap {
  kind: GapKind;
  neededBy: number;
  variable: VarVersion | null;
  controlId: number | null;
  message: string;
}

export interface SliceEdge {
  from: number;
  to: number;
  kind: 'data' | 'control';
  variable: VarVersion | null;
}

export interface SliceResult {
  criterion: number;
  events: LogEvent[];
  edges: SliceEdge[];
  gaps: Gap[];
}

export type ReplayVerdict = 'match' | 'mismatch' | 'indeterminate';

export interface ReplayReport {
  verdict: ReplayVerdict;
  assertionId: number;
  expected: Value;
  actual: Value | null;
  unknownEvents: number[];
}
