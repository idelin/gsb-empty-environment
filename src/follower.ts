import type { Clock } from "./clock.ts";
import type { HeartbeatReply, HeartbeatRequest } from "./transport.ts";

/**
 * 从节点：响应主节点心跳，并承诺在租约期内不选举新主。
 */
export class FollowerNode {
  readonly id: string;
  private clock: Clock;
  private promisedUntil = -Infinity;

  constructor(id: string, clock: Clock) {
    this.id = id;
    this.clock = clock;
  }

  handleHeartbeat(req: HeartbeatRequest): HeartbeatReply {
    const promisedUntil = this.clock.now() + req.requestedLeaseMs;
    this.promisedUntil = Math.max(this.promisedUntil, promisedUntil);
    return { followerId: this.id, ok: true, promisedUntil };
  }

  /** 是否仍受租约承诺约束（不得为其他主节点投票）。 */
  isLeaseBound(): boolean {
    return this.clock.now() < this.promisedUntil;
  }
}
