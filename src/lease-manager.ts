import type { Clock } from "./clock.ts";
import type { ClockDriftMonitor } from "./drift-monitor.ts";

export type Role = "idle" | "primary" | "demoted";

export type DemotionReason =
  | "safe-window-expired"
  | "clock-drift-exceeded"
  | "manual";

export class FencingTokenIssuer {
  private current = 0;

  next(): number {
    this.current += 1;
    return this.current;
  }
}

export interface LeaseConfig {
  leaseDurationMs: number;
  maxClockDriftMs: number;
  maxRttMs?: number;
  tokenIssuer?: FencingTokenIssuer;
}

export interface LeaseStatus {
  role: Role;
  fencingToken: number;
  elapsedMs: number | null;
  remainingSafeMs: number | null;
  demotionReason: DemotionReason | null;
}

export interface WriteReceipt {
  op: string;
  fencingToken: number;
  elapsedMs: number;
}

export type DemoteListener = (reason: DemotionReason, fencingToken: number) => void;

export class LeaseViolationError extends Error {
  readonly status: LeaseStatus;

  constructor(message: string, status: LeaseStatus) {
    super(message);
    this.name = "LeaseViolationError";
    this.status = status;
  }
}

export class LeaseManager {
  private readonly config: LeaseConfig;
  private readonly clock: Clock;
  private readonly driftMonitor: ClockDriftMonitor | undefined;
  readonly safeWindowMs: number;

  private role: Role = "idle";
  private acquiredAtMs: number | null = null;
  private fencingToken = 0;
  private demotionReason: DemotionReason | null = null;
  private readonly demoteListeners = new Set<DemoteListener>();

  constructor(config: LeaseConfig, clock: Clock, driftMonitor?: ClockDriftMonitor) {
    this.config = config;
    this.clock = clock;
    this.driftMonitor = driftMonitor;
    this.safeWindowMs =
      config.leaseDurationMs - config.maxClockDriftMs - (config.maxRttMs ?? 0);
    if (this.safeWindowMs <= 0) {
      throw new Error(
        `lease TTL ${config.leaseDurationMs}ms is too short for drift ` +
          `${config.maxClockDriftMs}ms + RTT ${config.maxRttMs ?? 0}ms margins`,
      );
    }
    this.driftMonitor?.onUnsafe(() => {
      if (this.role === "primary") {
        this.demote("clock-drift-exceeded");
      }
    });
    const adjustable = clock as { onAdjust?: (listener: () => void) => unknown };
    adjustable.onAdjust?.(() => this.performSafetyCheck());
  }

  acquire(): number {
    this.role = "primary";
    this.acquiredAtMs = this.clock.now();
    this.demotionReason = null;
    this.fencingToken = this.config.tokenIssuer?.next() ?? this.fencingToken + 1;
    this.driftMonitor?.resetBaseline();
    return this.fencingToken;
  }

  renew(): number {
    this.assertPrimary("renew");
    this.assertWithinSafeWindow("renew");
    this.acquiredAtMs = this.clock.now();
    this.driftMonitor?.resetBaseline();
    return this.fencingToken;
  }

  write(op: string): WriteReceipt {
    this.assertPrimary("write");
    this.assertWithinSafeWindow("write");
    return {
      op,
      fencingToken: this.fencingToken,
      elapsedMs: this.elapsedMs(),
    };
  }

  canWrite(): boolean {
    return this.role === "primary" && this.elapsedMs() < this.safeWindowMs;
  }

  performSafetyCheck(): void {
    if (this.role !== "primary") {
      return;
    }
    this.driftMonitor?.sample();
    if (this.role === "primary" && this.elapsedMs() >= this.safeWindowMs) {
      this.demote("safe-window-expired");
    }
  }

  status(): LeaseStatus {
    const elapsed = this.acquiredAtMs === null ? null : this.elapsedMs();
    return {
      role: this.role,
      fencingToken: this.fencingToken,
      elapsedMs: elapsed,
      remainingSafeMs: elapsed === null ? null : Math.max(0, this.safeWindowMs - elapsed),
      demotionReason: this.demotionReason,
    };
  }

  onDemote(listener: DemoteListener): () => void {
    this.demoteListeners.add(listener);
    return () => {
      this.demoteListeners.delete(listener);
    };
  }

  private elapsedMs(): number {
    if (this.acquiredAtMs === null) {
      return 0;
    }
    return this.clock.now() - this.acquiredAtMs;
  }

  private assertPrimary(action: string): void {
    if (this.role !== "primary") {
      throw new LeaseViolationError(
        `cannot ${action}: role is "${this.role}" (demotion reason: ${this.demotionReason ?? "none"})`,
        this.status(),
      );
    }
  }

  private assertWithinSafeWindow(action: string): void {
    if (this.elapsedMs() >= this.safeWindowMs) {
      this.demote("safe-window-expired");
      throw new LeaseViolationError(
        `cannot ${action}: lease safe window of ${this.safeWindowMs}ms has elapsed`,
        this.status(),
      );
    }
  }

  private demote(reason: DemotionReason): void {
    if (this.role !== "primary") {
      return;
    }
    this.role = "demoted";
    this.demotionReason = reason;
    for (const listener of this.demoteListeners) {
      listener(reason, this.fencingToken);
    }
  }
}
