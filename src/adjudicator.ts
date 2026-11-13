import {
  OPAQUE_ORIGIN,
  isSameOrigin,
  parseOrigin,
  resolveUrl,
} from './origin.ts';
import {
  COMPILED_POLICY_BRAND,
  type CompiledPolicy,
  type FencePolicy,
  compilePolicy,
} from './policy.ts';

/** Kind of access the fenced document attempted. */
export type OperationKind =
  | 'outer-url-read'
  | 'cross-context-message'
  | 'network-request';

/** Machine-readable reasons backing every decision. */
export type DecisionReason =
  | 'outer-url-disclosed'
  | 'top-communication-disabled'
  | 'unparseable-url'
  | 'opaque-origin'
  | 'origin-not-allowlisted'
  | 'same-origin-allowed'
  | 'allowlisted-origin'
  | 'self-message-allowed';

/** Context attached to every adjudication, useful for violation reporting. */
export interface FenceContext {
  /** What the document tried to do. */
  kind: OperationKind;
  /** Property/API being accessed for reads, e.g. `window.top.location.href`. */
  property?: string;
  /** Raw URL argument for network/message operations. */
  targetUrl?: string;
  /** Which window the message is aimed at: `top`, `parent`, `frameElement`... */
  targetContext?: string;
  /** Network API that initiated the request. */
  channel?: string;
}

export interface FenceDecision {
  allowed: boolean;
  reason: DecisionReason;
  kind: OperationKind;
  /** Resolved absolute URL when the operation carried one. */
  resolvedUrl?: string;
  /** Serialized origin of the resolved URL when one exists. */
  targetOrigin?: string;
}

/** Error thrown by the DOM enforcer when an operation is denied. */
export class FenceViolationError extends Error {
  readonly decision: FenceDecision;
  readonly context: FenceContext;

  constructor(decision: FenceDecision, context: FenceContext) {
    super(
      `Fenced frame blocked ${context.kind}` +
        `${context.property ? ` (${context.property})` : ''}: ${decision.reason}`,
    );
    this.name = 'FenceViolationError';
    this.decision = decision;
    this.context = context;
  }
}

function deny(
  kind: OperationKind,
  reason: DecisionReason,
  extra?: Partial<FenceDecision>,
): FenceDecision {
  return { allowed: false, reason, kind, ...extra };
}

function allow(
  kind: OperationKind,
  reason: DecisionReason,
  extra?: Partial<FenceDecision>,
): FenceDecision {
  return { allowed: true, reason, kind, ...extra };
}

/**
 * Pure, side-effect-free fence adjudicator.
 *
 * The embedding shell (or the optional DOM enforcer) calls one of the
 * `adjudicate*` methods for every sensitive operation and enforces the
 * returned {@link FenceDecision}.
 */
export class FenceAdjudicator {
  readonly policy: CompiledPolicy;

  constructor(policy: FencePolicy | CompiledPolicy) {
    this.policy = COMPILED_POLICY_BRAND in policy
      ? (policy as CompiledPolicy)
      : compilePolicy(policy as FencePolicy);
  }

  /**
   * Any attempt to read the embedding document's address.
   *
   * Fenced frames never expose the outer URL, so this is structurally denied
   * regardless of policy.
   */
  adjudicateOuterUrlRead(property: string): FenceDecision {
    return deny('outer-url-read', 'outer-url-disclosed', { property });
  }

  /**
   * `postMessage` style cross-document communication.
   *
   * Messages aimed at `top`/`parent`/`frameElement` are denied: the fence
   * cannot talk to the embedder. Messages that stay inside the fence
   * (target `self`) remain allowed.
   */
  adjudicateMessage(context: FenceContext): FenceDecision {
    const target = context.targetContext ?? 'top';
    if (target === 'self') {
      return allow('cross-context-message', 'self-message-allowed');
    }
    return deny('cross-context-message', 'top-communication-disabled', {
      targetOrigin: context.targetUrl,
    });
  }

  /**
   * Network egress check.
   *
   * Same-origin requests always pass; cross-origin requests pass only when
   * the destination origin appears in `policy.allowedOrigins`. Relative URLs
   * resolve against the fence document base; opaque/unparseable URLs fail.
   */
  adjudicateNetworkRequest(rawUrl: string, channel = 'fetch'): FenceDecision {
    const kind: OperationKind = 'network-request';
    let url: URL;
    try {
      url = resolveUrl(rawUrl, this.policy.documentBaseUrl);
    } catch {
      return deny(kind, 'unparseable-url', { targetUrl: rawUrl });
    }

    const targetOrigin = url.origin;
    const resolved: Partial<FenceDecision> = {
      resolvedUrl: url.href,
      targetOrigin,
    };

    if (targetOrigin === OPAQUE_ORIGIN) {
      return deny(kind, 'opaque-origin', resolved);
    }
    if (isSameOrigin(targetOrigin, this.policy.fenceOrigin)) {
      return allow(kind, 'same-origin-allowed', resolved);
    }
    if (this.policy.allowedOrigins.has(targetOrigin)) {
      return allow(kind, 'allowlisted-origin', resolved);
    }
    return deny(kind, 'origin-not-allowlisted', resolved);
  }

  /** Single dispatch entry point used by the DOM enforcer. */
  adjudicate(context: FenceContext): FenceDecision {
    switch (context.kind) {
      case 'outer-url-read':
        return this.adjudicateOuterUrlRead(context.property ?? '<unknown>');
      case 'cross-context-message':
        return this.adjudicateMessage(context);
      case 'network-request':
        return this.adjudicateNetworkRequest(
          context.targetUrl ?? '',
          context.channel,
        );
    }
  }
}

/** Convenience factory matching the "importable library" entry point. */
export function createAdjudicator(policy: FencePolicy): FenceAdjudicator {
  return new FenceAdjudicator(compilePolicy(policy));
}

export { parseOrigin };
