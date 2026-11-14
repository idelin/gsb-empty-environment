import { verifyForwardedRequest } from "./backend.ts";
import { BoundaryError } from "./errors.ts";
import { MessageBoundaryMiddleware } from "./middleware.ts";
import { parseHttp1Message } from "./message.ts";
import type {
  BoundaryPolicy,
  ForwardedRequest,
  Rejection,
} from "./types.ts";

export type BackendHandler = (
  request: ForwardedRequest,
) => void | Promise<void>;

export interface ProxyConnectionOptions {
  readonly backend: BackendHandler;
  readonly policy?: BoundaryPolicy;
  readonly middleware?: MessageBoundaryMiddleware;
  readonly onReject?: (rejection: Rejection) => void | Promise<void>;
  readonly onClose?: (info: CloseInfo) => void | Promise<void>;
  readonly onUpgradeData?: (chunk: Buffer) => void | Promise<void>;
  /**
   * Verify the normalized headers against the boundary before invoking the
   * backend. Enabled by default.
   */
  readonly verifyBoundary?: boolean;
}

export interface CloseInfo {
  readonly reason: string;
  readonly requestNumber: number;
  readonly residualBytes: number;
  readonly error: BoundaryError | null;
}

type ConnectionState = "idle" | "tunnel" | "closed";

/**
 * One inbound HTTP/1 connection.
 *
 * The state machine only parses a new request after the previous message was
 * consumed to its exact boundary and the input buffer was cleared. Bytes left
 * after that boundary are treated as residual, cleared, and the connection is
 * closed unless pipelining was explicitly enabled.
 */
export class ProxyConnection {
  readonly middleware: MessageBoundaryMiddleware;
  readonly backend: BackendHandler;
  readonly onReject: ((rejection: Rejection) => void | Promise<void>) | undefined;
  readonly onClose: ((info: CloseInfo) => void | Promise<void>) | undefined;
  readonly onUpgradeData: ((chunk: Buffer) => void | Promise<void>) | undefined;
  readonly verifyBoundary: boolean;

  private buffer: Buffer = Buffer.alloc(0);
  private state: ConnectionState = "idle";
  private requestNumberValue = 0;
  private residualBytesValue = 0;
  private lastErrorValue: BoundaryError | null = null;
  private accepting = true;
  private drainPromise: Promise<void> | null = null;

  constructor(options: ProxyConnectionOptions) {
    this.middleware =
      options.middleware ?? new MessageBoundaryMiddleware(options.policy ?? {});
    this.backend = options.backend;
    this.onReject = options.onReject;
    this.onClose = options.onClose;
    this.onUpgradeData = options.onUpgradeData;
    this.verifyBoundary = options.verifyBoundary ?? true;
  }

  get stateValue(): ConnectionState {
    return this.state;
  }

  get requestNumber(): number {
    return this.requestNumberValue;
  }

  get residualBytes(): number {
    return this.residualBytesValue;
  }

  get lastError(): BoundaryError | null {
    return this.lastErrorValue;
  }

  get bufferedBytes(): number {
    return this.buffer.length;
  }

  async feed(chunk: Buffer | string): Promise<void> {
    if (!this.accepting || this.state === "closed") return;

    const bytes = typeof chunk === "string" ? Buffer.from(chunk, "latin1") : chunk;
    if (bytes.length === 0) return;

    if (this.state === "tunnel") {
      await this.onUpgradeData?.(Buffer.from(bytes));
      return;
    }

    this.buffer =
      this.buffer.length === 0
        ? Buffer.from(bytes)
        : Buffer.concat([this.buffer, bytes]);
    await this.drain();
  }

  /**
   * Client half-close / FIN. Complete messages already in the buffer may be
   * forwarded, but a partial message is rejected and never reinterpreted as a
   * new implicit request.
   */
  async end(): Promise<void> {
    if (this.state === "closed") return;
    this.accepting = false;

    if (this.state === "idle" && this.buffer.length > 0) {
      await this.drain();
    }
    if (this.state === "closed") return;
    if (this.state === "tunnel") {
      await this.close("half_close");
      return;
    }
    if (this.buffer.length > 0) {
      await this.reject(
        new BoundaryError(
          "INCOMPLETE_MESSAGE",
          "connection ended inside a request",
        ),
      );
      return;
    }
    await this.close("half_close");
  }

  /**
   * Timeout closes the inbound connection and clears all partial bytes. No
   * implicit request can be synthesized from the remaining buffer.
   */
  async timeout(): Promise<void> {
    if (this.state === "closed") return;
    this.accepting = false;
    this.residualBytesValue = this.buffer.length;
    this.buffer = Buffer.alloc(0);
    await this.close("timeout");
  }

  async close(reason = "closed"): Promise<void> {
    if (this.state === "closed") return;
    this.accepting = false;
    this.state = "closed";
    this.buffer = Buffer.alloc(0);
    await this.onClose?.({
      reason,
      requestNumber: this.requestNumberValue,
      residualBytes: this.residualBytesValue,
      error: this.lastErrorValue,
    });
  }

  private async drain(): Promise<void> {
    if (this.drainPromise !== null) {
      await this.drainPromise;
      return;
    }
    this.drainPromise = this.drainLoop().finally(() => {
      this.drainPromise = null;
    });
    await this.drainPromise;
  }

  private async drainLoop(): Promise<void> {
    while (this.state === "idle" && this.buffer.length > 0) {
      let parsed;
      try {
        parsed = parseHttp1Message(this.buffer, 0, this.middleware.policy);
      } catch (error) {
        if (error instanceof BoundaryError) {
          await this.reject(error);
          return;
        }
        this.lastErrorValue = new BoundaryError(
          "INVALID_HEADER",
          error instanceof Error ? error.message : String(error),
        );
        await this.reject(this.lastErrorValue);
        return;
      }

      if (parsed.status === "incomplete") return;

      this.requestNumberValue += 1;
      const message = parsed.value;
      const request: ForwardedRequest = Object.freeze({
        requestNumber: this.requestNumberValue,
        method: message.head.method,
        target: message.head.target,
        version: message.head.version,
        headers: message.headers,
        body: message.body,
        boundary: message.boundary,
      });

      if (this.verifyBoundary) {
        try {
          verifyForwardedRequest(request);
        } catch (error) {
          if (error instanceof BoundaryError) {
            await this.reject(error);
            return;
          }
          throw error;
        }
      }

      try {
        await this.backend(request);
      } catch (error) {
        await this.close("backend_error");
        throw error;
      }
      if (this.state === "closed") return;

      const hasResidual = message.nextOffset < this.buffer.length;
      if (message.boundary.kind === "upgrade") {
        const upgradeBytes = this.buffer.subarray(message.nextOffset);
        this.buffer = Buffer.alloc(0);
        this.state = "tunnel";
        if (upgradeBytes.length > 0) {
          await this.onUpgradeData?.(Buffer.from(upgradeBytes));
        }
        return;
      }

      if (hasResidual && !this.middleware.policy.allowPipelining) {
        this.residualBytesValue = this.buffer.length - message.nextOffset;
        const error = new BoundaryError(
          "RESIDUAL_AFTER_MESSAGE",
          `${this.residualBytesValue} byte(s) remained after the message boundary`,
        );
        // Clear the buffer before any possible reuse. Residual bytes are never
        // fed back into the parser as an implicit request.
        this.buffer = Buffer.alloc(0);
        await this.reject(error, true);
        return;
      }

      // Always clear the consumed buffer before the next reuse.
      this.buffer =
        message.nextOffset === this.buffer.length
          ? Buffer.alloc(0)
          : Buffer.from(this.buffer.subarray(message.nextOffset));
    }
  }

  private async reject(
    error: BoundaryError,
    alreadyCleared = false,
  ): Promise<void> {
    if (this.state === "closed") return;
    this.lastErrorValue = error;
    this.accepting = false;
    const bufferedBytes = alreadyCleared ? this.residualBytesValue : this.buffer.length;
    if (!alreadyCleared) {
      this.residualBytesValue = this.buffer.length;
      this.buffer = Buffer.alloc(0);
    }

    const rejection: Rejection = Object.freeze({
      error,
      requestNumber: this.requestNumberValue,
      bufferedBytes,
      closeConnection: true,
    });

    try {
      await this.onReject?.(rejection);
    } finally {
      await this.close("rejected");
    }
  }
}
