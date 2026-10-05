export interface TraceEvent {
  action: string;
  args?: Record<string, unknown>;
  result?: unknown;
  status?: 'ok' | 'error';
  error?: string;
  seq?: number;
  meta?: Record<string, unknown>;
}

export interface RunSegment {
  kind: 'run';
  events: TraceEvent[];
}

export interface LoopSegment {
  kind: 'loop';
  body: TraceEvent[];
  times: number;
}

export type Segment = RunSegment | LoopSegment;

export type TraceInput =
  | TraceEvent[]
  | Segment[]
  | { events: TraceEvent[] }
  | { segments: Segment[] };

export interface CompareOptions {
  observable: string[];
  ignoreKeys?: string[];
  numericTolerance?: number;
  maxWindow?: number;
  lookahead?: number;
  maxValueDiffs?: number;
}

export interface ValueDiff {
  path: string;
  left: unknown;
  right: unknown;
}

export interface WindowSide {
  lastMatchedObservable: TraceEvent | null;
  internal: TraceEvent[];
  internalTotal: number;
  truncated: boolean;
  divergingObservable: TraceEvent | null;
}

export type DivergenceKind = 'value-mismatch' | 'missing-action' | 'order-mismatch';

export interface Divergence {
  kind: DivergenceKind;
  observableIndex: number;
  left: TraceEvent | null;
  right: TraceEvent | null;
  valueDiffs: ValueDiff[];
  valueDiffsTruncated: boolean;
  foundInLeftAt: number | null;
  foundInRightAt: number | null;
  remainingObservables: number | null;
  detail: string;
  window: { left: WindowSide; right: WindowSide };
}

export interface SideStats {
  totalEvents: number;
  consumedEvents: number;
  internalEvents: number;
  matchedObservables: number;
}

export interface DiffReport {
  equal: boolean;
  matchedObservables: number;
  divergence: Divergence | null;
  stats: { left: SideStats; right: SideStats };
}
