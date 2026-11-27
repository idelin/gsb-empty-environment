import type { AggregateVerdict } from "./aggregate.js";

export interface StateMachineConfig {
  /** Traffic percentages per stage, ascending, e.g. [5, 25, 50, 100]. */
  stages: number[];
  /** Consecutive PASS verdicts required to advance to the next stage. */
  passesPerStage: number;
  /** Roll back when consecutive INCONCLUSIVE verdicts exceed this count. */
  maxConsecutiveInconclusive: number;
}

export type MachineStatus = "PROMOTING" | "ROLLED_BACK" | "COMPLETED";

export interface MachineState {
  stageIndex: number;
  trafficPercent: number;
  consecutivePasses: number;
  consecutiveInconclusive: number;
  status: MachineStatus;
}

/**
 * Multi-stage canary traffic promotion state machine.
 *
 * - PASS: increments the consecutive-pass counter; after `passesPerStage`
 *   consecutive passes the machine advances to the next stage (and completes
 *   after the final stage).
 * - FAIL: immediate rollback to 0% traffic.
 * - INCONCLUSIVE: tolerated up to `maxConsecutiveInconclusive` in a row;
 *   exceeding that rolls back to 0%.
 */
export class CanaryStateMachine {
  private readonly config: StateMachineConfig;
  private current: MachineState;

  constructor(config: StateMachineConfig) {
    if (config.stages.length === 0) {
      throw new Error("at least one traffic stage is required");
    }
    if (config.passesPerStage < 1) {
      throw new Error("passesPerStage must be >= 1");
    }
    this.config = config;
    this.current = {
      stageIndex: 0,
      trafficPercent: config.stages[0],
      consecutivePasses: 0,
      consecutiveInconclusive: 0,
      status: "PROMOTING",
    };
  }

  get state(): MachineState {
    return { ...this.current };
  }

  step(verdict: AggregateVerdict): MachineState {
    if (this.current.status !== "PROMOTING") return this.state;

    if (verdict === "FAIL") {
      this.rollback();
      return this.state;
    }

    if (verdict === "INCONCLUSIVE") {
      this.current.consecutiveInconclusive++;
      this.current.consecutivePasses = 0;
      if (
        this.current.consecutiveInconclusive >
        this.config.maxConsecutiveInconclusive
      ) {
        this.rollback();
      }
      return this.state;
    }

    // PASS
    this.current.consecutivePasses++;
    this.current.consecutiveInconclusive = 0;
    if (this.current.consecutivePasses >= this.config.passesPerStage) {
      if (this.current.stageIndex === this.config.stages.length - 1) {
        this.current.status = "COMPLETED";
      } else {
        this.current.stageIndex++;
        this.current.trafficPercent =
          this.config.stages[this.current.stageIndex];
      }
      this.current.consecutivePasses = 0;
    }
    return this.state;
  }

  private rollback(): void {
    this.current = {
      stageIndex: 0,
      trafficPercent: 0,
      consecutivePasses: 0,
      consecutiveInconclusive: 0,
      status: "ROLLED_BACK",
    };
  }
}
