import { PhiAccrualDetector } from "./phiAccrualDetector.ts";

export type LeaseState = "held" | "suspected" | "reclaimed";

export interface LeaseEvent {
  at: number;
  phi: number;
  state: LeaseState;
}

/**
 * 租约管理器：把 φ 怀疑度映射为租约生命周期。
 *  - held：φ < threshold，租约有效；
 *  - φ >= threshold：果断回收租约并触发回调（重选/故障转移的钩子）。
 */
export class LeaseManager {
  private readonly detector: PhiAccrualDetector;
  private readonly threshold: number;
  private readonly now: () => number;
  private state: LeaseState = "held";
  private listeners: Array<(e: LeaseEvent) => void> = [];

  constructor(
    detector: PhiAccrualDetector,
    options: { phiThreshold?: number; now?: () => number } = {},
  ) {
    this.detector = detector;
    this.threshold = options.phiThreshold ?? 8;
    this.now = options.now ?? Date.now;
  }

  get currentState(): LeaseState {
    return this.state;
  }

  onReclaim(listener: (e: LeaseEvent) => void): void {
    this.listeners.push(listener);
  }

  /** 心跳到达时调用，续租 */
  renew(atMs?: number): void {
    this.detector.heartbeat(atMs);
  }

  /**
   * 周期性检查：返回当前状态。
   * 一旦 φ 超过阈值即回收租约（幂等，只触发一次回调）。
   */
  check(atMs?: number): LeaseState {
    if (this.state === "reclaimed") return this.state;
    const at = atMs ?? this.now();
    const phi = this.detector.phi(at);
    if (phi >= this.threshold) {
      this.state = "reclaimed";
      const event: LeaseEvent = { at, phi, state: this.state };
      for (const l of this.listeners) l(event);
    }
    return this.state;
  }
}
