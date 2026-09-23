/** Error taxonomy for scope lifecycle failures. */

export class ScopeClosedError extends Error {
  readonly scopeId: string;
  constructor(scopeId: string) {
    super(`scope "${scopeId}" is closing or already closed`);
    this.name = "ScopeClosedError";
    this.scopeId = scopeId;
  }
}

export class AcquireCancelledError extends Error {
  readonly resourceName: string;
  constructor(resourceName: string) {
    super(`acquisition of resource "${resourceName}" was cancelled`);
    this.name = "AcquireCancelledError";
    this.resourceName = resourceName;
  }
}

export class DependencyCycleError extends Error {
  readonly cycle: string[];
  constructor(cycle: string[]) {
    super(`dependency cycle detected: ${cycle.join(" -> ")}`);
    this.name = "DependencyCycleError";
    this.cycle = cycle;
  }
}

export class ResourceReleaseError extends Error {
  readonly resourceId: string;
  constructor(resourceId: string, cause: unknown) {
    super(
      `failed to release resource "${resourceId}": ${
        cause instanceof Error ? cause.message : String(cause)
      }`,
      { cause },
    );
    this.name = "ResourceReleaseError";
    this.resourceId = resourceId;
  }
}

/**
 * Deterministic aggregate thrown by Scope.close() when anything went wrong.
 * Always an AggregateError: `errors` preserves collection order
 * (children first, then resources in release order, then late arrivals).
 */
export class ScopeCloseError extends AggregateError {
  readonly scopeId: string;
  constructor(scopeId: string, errors: Iterable<unknown>) {
    const list = [...errors];
    super(list, `scope "${scopeId}" closed with ${list.length} error(s)`);
    this.name = "ScopeCloseError";
    this.scopeId = scopeId;
  }
}
