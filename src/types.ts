export type Segment =
  | { kind: 'run'; duration: number }
  | { kind: 'lock'; lock: string }
  | { kind: 'unlock'; lock: string };

export interface TaskSpec {
  id: string;
  priority: number;
  releaseTime: number;
  lockTimeout?: number;
  segments: Segment[];
}

export interface Scenario {
  name: string;
  maxTicks?: number;
  tasks: TaskSpec[];
}

export type TaskStatus =
  | 'pending'
  | 'ready'
  | 'running'
  | 'blocked'
  | 'done'
  | 'timed_out'
  | 'deadlocked';

export interface BlockedInfo {
  id: string;
  lock: string;
  holder: string | null;
  waited: number;
  timeout?: number;
}

export interface TickRecord {
  tick: number;
  running: string | null;
  basePriority: number | null;
  effectivePriority: number | null;
  events: string[];
  blocked: BlockedInfo[];
}

export interface TaskSummary {
  id: string;
  status: TaskStatus;
  finishTick: number | null;
}

export interface SimulationResult {
  name: string;
  ticks: TickRecord[];
  tasks: TaskSummary[];
}
