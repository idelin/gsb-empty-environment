export type StepType = "delay" | "packet-loss" | "restart";

export interface DelayStep {
  type: "delay";
  ms: number;
}

export interface PacketLossStep {
  type: "packet-loss";
  node: string;
  rate: number;
}

export interface RestartStep {
  type: "restart";
  node: string;
}

export type Step = DelayStep | PacketLossStep | RestartStep;

export interface ScriptFile {
  name?: string;
  nodes?: string[];
  seed?: number;
  steps: Step[];
}

export type ReplayStatus = "fail" | "pass" | "invalid";

export interface ReplaySummary {
  status: ReplayStatus;
  /** Violated assertion description, present when status === "fail". */
  assertion?: string;
  /** Virtual time (ms) at which the assertion fired. */
  violatedAtMs?: number;
  /** Why the script is not executable, present when status === "invalid". */
  invalidReason?: string;
  simulatedMs: number;
  events: number;
  finalLeaders: string[];
  finalTerms: Record<string, number>;
}

export interface ReplayHarness {
  replay(steps: Step[]): ReplaySummary;
}
