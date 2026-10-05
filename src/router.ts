import { SimulatedNode, ReadResult } from "./node";
import { RouterOptions, StalePolicy, SleepFn, defaultSleep } from "./policies";
import { Session } from "./session";

export class SessionTimeoutError extends Error {
  readonly partition: string;
  readonly requiredVersion: number;
  readonly policy: StalePolicy;

  constructor(partition: string, requiredVersion: number, policy: StalePolicy, detail: string) {
    super(
      `session read timed out on partition "${partition}": ` +
        `required version >= ${requiredVersion} under policy "${policy}" (${detail})`
    );
    this.name = "SessionTimeoutError";
    this.partition = partition;
    this.requiredVersion = requiredVersion;
    this.policy = policy;
  }
}

/**
 * Routes session reads across replica nodes. The session watermark is
 * the floor: a response is only returned to the caller when its version
 * is >= the session's required version for that partition. When the
 * contacted replica lags, the configured StalePolicy decides whether to
 * wait for it, switch to another node, or fail with a timeout.
 */
export class SessionRouter {
  private readonly nodes: SimulatedNode[];
  private readonly options: Required<RouterOptions>;
  private readonly sleep: SleepFn;

  constructor(nodes: SimulatedNode[], options: RouterOptions) {
    if (nodes.length === 0) {
      throw new Error("SessionRouter requires at least one node");
    }
    this.nodes = nodes.slice();
    this.options = {
      policy: options.policy,
      timeoutMs: options.timeoutMs,
      pollIntervalMs: options.pollIntervalMs !== undefined ? options.pollIntervalMs : 10,
      sleep: options.sleep || defaultSleep,
    };
    this.sleep = this.options.sleep;
  }

  /**
   * Read a partition within a session. Guaranteed: the returned version
   * is >= every version previously returned to this session for that
   * partition. The response watermark is recorded back into the session.
   */
  async read(session: Session, partition: string): Promise<ReadResult> {
    const required = session.requiredVersion(partition);
    const deadline = Date.now() + this.options.timeoutMs;
    switch (this.options.policy) {
      case StalePolicy.WAIT:
        return this.readWithWait(session, partition, required, deadline);
      case StalePolicy.SWITCH:
        return this.readWithSwitch(session, partition, required, deadline);
      case StalePolicy.TIMEOUT:
        return this.readOnce(session, partition, required, deadline);
    }
  }

  /** TIMEOUT policy: one attempt on the primary node, no waiting. */
  private async readOnce(
    session: Session,
    partition: string,
    required: number,
    deadline: number
  ): Promise<ReadResult> {
    const result = await this.nodes[0].read(partition);
    session.recordResponse(partition, result.version);
    if (Date.now() > deadline) {
      throw new SessionTimeoutError(partition, required, this.options.policy, "deadline exceeded");
    }
    if (result.version < required) {
      throw new SessionTimeoutError(
        partition,
        required,
        this.options.policy,
        `node ${result.nodeId} is at version ${result.version}`
      );
    }
    return result;
  }

  /** SWITCH policy: walk the node list until a fresh-enough replica answers. */
  private async readWithSwitch(
    session: Session,
    partition: string,
    required: number,
    deadline: number
  ): Promise<ReadResult> {
    const attempted: string[] = [];
    for (const node of this.nodes) {
      if (Date.now() > deadline) {
        break;
      }
      const result = await node.read(partition);
      session.recordResponse(partition, result.version);
      attempted.push(`${result.nodeId}@${result.version}`);
      if (result.version >= required && Date.now() <= deadline) {
        return result;
      }
    }
    throw new SessionTimeoutError(
      partition,
      required,
      this.options.policy,
      `all replicas stale or unreachable: ${attempted.join(", ") || "none attempted"}`
    );
  }

  /** WAIT policy: poll the primary node until it catches up. */
  private async readWithWait(
    session: Session,
    partition: string,
    required: number,
    deadline: number
  ): Promise<ReadResult> {
    const node = this.nodes[0];
    for (;;) {
      const result = await node.read(partition);
      session.recordResponse(partition, result.version);
      if (result.version >= required && Date.now() <= deadline) {
        return result;
      }
      const remaining = deadline - Date.now();
      if (remaining <= 0) {
        throw new SessionTimeoutError(
          partition,
          required,
          this.options.policy,
          `node ${result.nodeId} still at version ${result.version} at deadline`
        );
      }
      await this.sleep(Math.min(this.options.pollIntervalMs, remaining));
    }
  }
}
