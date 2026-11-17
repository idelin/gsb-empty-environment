import type {
  ActivationBinding,
  ClipboardActionDependencies,
  ClipboardActionFailure,
  ClipboardActionOptions,
  ClipboardActionResult,
  ClipboardEnvironment,
  ClipboardFailureReason,
  ClipboardOperation,
  ClipboardPermissionDecision,
  ClipboardPermissionGranter,
  ClipboardPermissionRequest,
  DocumentBinding,
  FocusBinding,
  SystemClipboard,
  AbortSignalLike,
} from "./types.ts";

interface Snapshot {
  readonly document: DocumentBinding;
  readonly focus: FocusBinding;
  readonly activation: ActivationBinding | null;
}

interface PendingRequest {
  settled: boolean;
}

class ActionAborted extends Error {
  constructor() {
    super("clipboard action cancelled");
    this.name = "ActionAborted";
  }
}

const ABORTED = new ActionAborted();

let fallbackIdentifierSequence = 0;
let identifierSequence = 0;

function defaultIdentifier(): string {
  const cryptoObject = (
    globalThis as unknown as {
      crypto?: { randomUUID?: () => string };
    }
  ).crypto;

  if (cryptoObject && typeof cryptoObject.randomUUID === "function") {
    return cryptoObject.randomUUID();
  }

  fallbackIdentifierSequence += 1;
  return [
    Date.now().toString(36),
    fallbackIdentifierSequence.toString(36),
    Math.random().toString(36).slice(2),
  ].join("-");
}

function failure(
  reason: ClipboardFailureReason,
  requestId?: string,
  error?: unknown,
): ClipboardActionFailure {
  if (error === undefined) {
    return { ok: false, reason, requestId };
  }
  return { ok: false, reason, requestId, error };
}

function copyDocument(document: DocumentBinding): DocumentBinding {
  return Object.freeze({
    tabId: document.tabId,
    documentId: document.documentId,
    generation: document.generation,
  });
}

function copyFocus(focus: FocusBinding): FocusBinding {
  return Object.freeze({
    generation: focus.generation,
    tabId: focus.tabId,
  });
}

function copyActivation(
  activation: ActivationBinding | null,
): ActivationBinding | null {
  if (activation === null) {
    return null;
  }
  return Object.freeze({
    activationId: activation.activationId,
    expiresAt: activation.expiresAt,
  });
}

/**
 * Performs clipboard reads and writes only after a permission callback has
 * been matched against the exact document, focus and activation that existed
 * when the request was made.
 *
 * The check immediately before invoking the system clipboard is deliberately
 * synchronous. There is no `await`, callback or microtask between the final
 * validation and the clipboard call.
 */
export class ClipboardActions {
  private readonly environment: ClipboardEnvironment;
  private readonly permissions: ClipboardPermissionGranter;
  private readonly systemClipboard: SystemClipboard;
  private readonly now: () => number;
  private readonly createIdentifier: () => string;
  private readonly pending = new Map<string, PendingRequest>();

  constructor(dependencies: ClipboardActionDependencies) {
    this.environment = dependencies.environment;
    this.permissions = dependencies.permissions;
    this.systemClipboard = dependencies.systemClipboard;
    this.now = dependencies.now ?? (() => this.environment.now());
    this.createIdentifier =
      dependencies.createRequestId ?? defaultIdentifier;
  }

  /** Number of actions currently waiting for a permission callback. */
  get pendingCount(): number {
    return this.pending.size;
  }

  /** Copies `text`, subject to the bound permission checks. */
  copy(
    text: string,
    options: ClipboardActionOptions = {},
  ): Promise<ClipboardActionResult<void>> {
    return this.run("write", options, () =>
      this.systemClipboard.writeText(text),
    );
  }

  /** Alias for `copy`, matching `navigator.clipboard.writeText`. */
  writeText(
    text: string,
    options: ClipboardActionOptions = {},
  ): Promise<ClipboardActionResult<void>> {
    return this.copy(text, options);
  }

  /** Reads text, subject to the bound permission checks. */
  read(
    options: ClipboardActionOptions = {},
  ): Promise<ClipboardActionResult<string>> {
    return this.run("read", options, () => this.systemClipboard.readText());
  }

  /** Alias for `read`. */
  paste(
    options: ClipboardActionOptions = {},
  ): Promise<ClipboardActionResult<string>> {
    return this.read(options);
  }

  /** Alias for `read`, matching `navigator.clipboard.readText`. */
  readText(
    options: ClipboardActionOptions = {},
  ): Promise<ClipboardActionResult<string>> {
    return this.read(options);
  }

  private async run<T>(
    operation: ClipboardOperation,
    options: ClipboardActionOptions,
    invoke: () => T | Promise<T>,
  ): Promise<ClipboardActionResult<T>> {
    const signal = options.signal ?? null;

    if (signal?.aborted) {
      return failure("cancelled");
    }

    let snapshot: Snapshot;
    let requestedAt: number;
    try {
      snapshot = this.capture();
      requestedAt = this.now();
    } catch (error) {
      return failure("environment-error", undefined, error);
    }

    if (snapshot.activation === null) {
      return failure("inactive");
    }
    if (requestedAt >= snapshot.activation.expiresAt) {
      return failure("activation-expired");
    }
    if (
      snapshot.focus.tabId === null ||
      snapshot.focus.tabId !== snapshot.document.tabId
    ) {
      return failure("not-focused");
    }

    let requestId: string;
    let token: string;
    let request: ClipboardPermissionRequest;
    try {
      requestId = this.nextIdentifier("request");
      token = this.nextIdentifier("token");
      request = Object.freeze({
        requestId,
        token,
        operation,
        tabId: snapshot.document.tabId,
        documentId: snapshot.document.documentId,
        documentGeneration: snapshot.document.generation,
        focusGeneration: snapshot.focus.generation,
        activationId: snapshot.activation.activationId,
        activationExpiresAt: snapshot.activation.expiresAt,
        requestedAt,
      });
    } catch (error) {
      return failure("permission-error", undefined, error);
    }

    const pendingRequest: PendingRequest = { settled: false };
    this.pending.set(requestId, pendingRequest);

    let decision: ClipboardPermissionDecision;
    try {
      decision = await this.awaitDecision(request, pendingRequest, signal);
    } catch (error) {
      this.pending.delete(requestId);
      if (error === ABORTED) {
        this.notifyCancelled(requestId, token);
        return failure("cancelled", requestId);
      }
      return failure("permission-error", requestId, error);
    }

    if (pendingRequest.settled) {
      this.pending.delete(requestId);
      return failure("cancelled", requestId);
    }

    // Mark this request as consumed before doing any further work. A duplicate
    // callback can therefore never reach the clipboard, while other requests
    // keep their own independent records.
    pendingRequest.settled = true;
    this.pending.delete(requestId);

    if (
      decision === null ||
      decision === undefined ||
      decision.requestId !== requestId
    ) {
      return failure("permission-mismatch", requestId);
    }
    if (decision.status === "cancelled") {
      return failure("cancelled", requestId);
    }
    if (decision.status !== "granted") {
      return failure("permission-denied", requestId);
    }
    if (decision.token !== token) {
      return failure("permission-mismatch", requestId);
    }

    let revalidationFailure: ClipboardActionFailure | null;
    try {
      revalidationFailure = this.revalidate(request, signal);
    } catch (error) {
      return failure("environment-error", requestId, error);
    }
    if (revalidationFailure !== null) {
      return revalidationFailure;
    }
    if (signal?.aborted) {
      return failure("cancelled", requestId);
    }

    // SECURITY: `invoke()` is called synchronously here. Do not insert an
    // `await` between `revalidate` and this invocation, otherwise navigation
    // or a focus switch could slip into the gap.
    try {
      const value = await invoke();
      return { ok: true, value, requestId };
    } catch (error) {
      return failure("clipboard-error", requestId, error);
    }
  }

  private capture(): Snapshot {
    return {
      document: copyDocument(this.environment.getDocument()),
      focus: copyFocus(this.environment.getFocus()),
      activation: copyActivation(this.environment.getUserActivation()),
    };
  }

  /**
   * Re-check every part of the permission binding. Returning from this method
   * without a failure is the only authorization to touch the clipboard.
   */
  private revalidate(
    request: ClipboardPermissionRequest,
    signal: AbortSignalLike | null,
  ): ClipboardActionFailure | null {
    if (signal?.aborted) {
      return failure("cancelled", request.requestId);
    }

    const document = this.environment.getDocument();
    if (
      document.tabId !== request.tabId ||
      document.documentId !== request.documentId ||
      document.generation !== request.documentGeneration
    ) {
      return failure("navigation", request.requestId);
    }

    const focus = this.environment.getFocus();
    if (
      focus.generation !== request.focusGeneration ||
      focus.tabId !== request.tabId
    ) {
      return failure("focus-change", request.requestId);
    }

    const activation = this.environment.getUserActivation();
    if (activation === null) {
      return failure("activation-expired", request.requestId);
    }
    if (
      activation.activationId !== request.activationId ||
      activation.expiresAt !== request.activationExpiresAt
    ) {
      return failure("activation-changed", request.requestId);
    }
    if (this.now() >= activation.expiresAt) {
      return failure("activation-expired", request.requestId);
    }

    return null;
  }

  private async awaitDecision(
    request: ClipboardPermissionRequest,
    pendingRequest: PendingRequest,
    signal: AbortSignalLike | null,
  ): Promise<ClipboardPermissionDecision> {
    let permissionResult:
      | Promise<ClipboardPermissionDecision>
      | ClipboardPermissionDecision;

    try {
      permissionResult = this.permissions.request(request);
    } catch (error) {
      pendingRequest.settled = true;
      this.pending.delete(request.requestId);
      throw error;
    }

    const permissionPromise = Promise.resolve(permissionResult);

    if (signal === null) {
      return permissionPromise;
    }
    if (signal.aborted) {
      pendingRequest.settled = true;
      this.pending.delete(request.requestId);
      throw ABORTED;
    }

    return new Promise<ClipboardPermissionDecision>((resolve, reject) => {
      let finished = false;

      const cleanup = (): void => {
        signal.removeEventListener?.("abort", onAbort);
      };

      const onAbort = (): void => {
        if (finished) {
          return;
        }
        finished = true;
        pendingRequest.settled = true;
        this.pending.delete(request.requestId);
        cleanup();
        reject(ABORTED);
      };

      signal.addEventListener?.("abort", onAbort, { once: true });

      permissionPromise.then(
        (decision) => {
          if (finished) {
            return;
          }
          finished = true;
          cleanup();
          resolve(decision);
        },
        (error: unknown) => {
          if (finished) {
            return;
          }
          finished = true;
          cleanup();
          reject(error);
        },
      );

      if (signal.aborted) {
        onAbort();
      }
    });
  }

  private nextIdentifier(kind: "request" | "token"): string {
    identifierSequence += 1;
    return [
      kind,
      this.createIdentifier(),
      identifierSequence.toString(36),
    ].join("-");
  }

  private notifyCancelled(requestId: string, token: string): void {
    try {
      this.permissions.cancel?.(requestId, token);
    } catch {
      // Cancellation is best effort. The action is already inert.
    }
  }
}

export function createClipboardActions(
  dependencies: ClipboardActionDependencies,
): ClipboardActions {
  return new ClipboardActions(dependencies);
}
