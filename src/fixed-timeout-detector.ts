export class FixedTimeoutDetector {
  private readonly timeoutMs: number;
  private lastHeartbeatAt: number | undefined;

  constructor(timeoutMs: number) {
    this.timeoutMs = timeoutMs;
  }

  reportHeartbeat(now: number = Date.now()): void {
    this.lastHeartbeatAt = now;
  }

  isAvailable(now: number = Date.now()): boolean {
    return this.lastHeartbeatAt !== undefined && now - this.lastHeartbeatAt <= this.timeoutMs;
  }
}
