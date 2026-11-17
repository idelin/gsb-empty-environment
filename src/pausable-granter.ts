import type {
  ClipboardPermissionDecision,
  ClipboardPermissionGranter,
  ClipboardPermissionRequest,
} from "./types.ts";

interface PendingCall {
  readonly request: ClipboardPermissionRequest;
  readonly promise: Promise<ClipboardPermissionDecision>;
  readonly resolve: (decision: ClipboardPermissionDecision) => void;
  settled: boolean;
}

/**
 * A deterministic permission grantor for tests and hostile-race simulations.
 *
 * Every prompt remains pending until `grant`, `deny`, `cancel`, or `settle` is
 * called for that exact request. Resolving one request can never resolve,
 * remove, or invalidate another one.
 */
export class PausableClipboardPermissionGranter
  implements ClipboardPermissionGranter
{
  private readonly calls = new Map<string, PendingCall>();
  private readonly order: string[] = [];
  private readonly known = new Set<string>();

  request(
    request: ClipboardPermissionRequest,
  ): Promise<ClipboardPermissionDecision> {
    if (this.known.has(request.requestId)) {
      return Promise.resolve({
        status: "denied",
        requestId: request.requestId,
        token: request.token,
        reason: "duplicate-request-id",
      });
    }

    this.known.add(request.requestId);

    let resolve: (decision: ClipboardPermissionDecision) => void = () => {};
    const promise = new Promise<ClipboardPermissionDecision>(
      (resolvePromise) => {
        resolve = resolvePromise;
      },
    );

    this.calls.set(request.requestId, {
      request,
      promise,
      resolve,
      settled: false,
    });
    this.order.push(request.requestId);

    return promise;
  }

  /** Requests that are still waiting for a host decision. */
  get pendingRequests(): readonly ClipboardPermissionRequest[] {
    const requests: ClipboardPermissionRequest[] = [];
    for (const requestId of this.order) {
      const call = this.calls.get(requestId);
      if (call !== undefined && !call.settled) {
        requests.push(call.request);
      }
    }
    return requests;
  }

  get pendingCount(): number {
    return this.calls.size;
  }

  /** Grants exactly one pending request. */
  grant(requestId: string): boolean {
    const call = this.calls.get(requestId);
    if (call === undefined) {
      return false;
    }
    return this.resolveCall(call, {
      status: "granted",
      requestId: call.request.requestId,
      token: call.request.token,
    });
  }

  /** Grants the oldest pending request and returns its id. */
  grantNext(): string | null {
    const request = this.pendingRequests[0];
    if (request === undefined) {
      return null;
    }
    return this.grant(request.requestId) ? request.requestId : null;
  }

  /** Denies exactly one pending request. */
  deny(requestId: string, reason = "denied"): boolean {
    const call = this.calls.get(requestId);
    if (call === undefined) {
      return false;
    }
    return this.resolveCall(call, {
      status: "denied",
      requestId: call.request.requestId,
      token: call.request.token,
      reason,
    });
  }

  /**
   * Reports that one prompt was dismissed or its action was cancelled.
   * A token, when supplied, must match the request's own token.
   */
  cancel(requestId: string, token?: string): boolean {
    const call = this.calls.get(requestId);
    if (call === undefined) {
      return false;
    }
    if (token !== undefined && token !== call.request.token) {
      return false;
    }
    return this.resolveCall(call, {
      status: "cancelled",
      requestId: call.request.requestId,
      token: call.request.token,
      reason: "cancelled",
    });
  }

  /**
   * Resolves a request with an arbitrary decision. Test-only escape hatch for
   * simulating a confused host that echoes the wrong request id or token.
   */
  settle(
    requestId: string,
    decision: ClipboardPermissionDecision,
  ): boolean {
    const call = this.calls.get(requestId);
    if (call === undefined) {
      return false;
    }
    return this.resolveCall(call, decision);
  }

  denyAll(reason = "denied"): number {
    let count = 0;
    for (const request of [...this.pendingRequests]) {
      if (this.deny(request.requestId, reason)) {
        count += 1;
      }
    }
    return count;
  }

  private resolveCall(
    call: PendingCall,
    decision: ClipboardPermissionDecision,
  ): boolean {
    if (call.settled) {
      return false;
    }
    call.settled = true;
    this.calls.delete(call.request.requestId);
    call.resolve(decision);
    return true;
  }
}

export function createPausablePermissionGranter(): PausableClipboardPermissionGranter {
  return new PausableClipboardPermissionGranter();
}
