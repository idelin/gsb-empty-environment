/**
 * Cross-Origin-Opener-Policy parsing and Browsing Context Group (BCG)
 * transition state machine.
 *
 * Implements the HTML standard "cross-origin opener policies" model:
 * https://html.spec.whatwg.org/multipage/origin.html#cross-origin-opener-policies
 *
 * COOP determines whether a navigation keeps the current browsing context
 * group or creates a fresh one, and whether a popup retains a live
 * `window.opener` link back to its opener.
 */

import {
  isSameOrigin,
  opaqueOrigin,
  parseOrigin,
  type Origin,
} from './origin.ts';

export type CoopValue =
  | 'unsafe-none'
  | 'same-origin-allow-popups'
  | 'same-origin';

export interface CoopPolicy {
  /** Effective enforced directive; report-only is tracked separately. */
  readonly value: CoopValue;
  readonly reportOnly: CoopValue | null;
  /** Raw policy tokens retained for diagnostics. */
  readonly reportEndpoints: readonly string[];
}

const COOP_VALUES: readonly CoopValue[] = [
  'unsafe-none',
  'same-origin-allow-popups',
  'same-origin',
];

function parseDirective(headerValue: string | null | undefined): CoopValue {
  if (!headerValue) return 'unsafe-none';
  const token = headerValue.split(';', 1)[0].trim().toLowerCase();
  return (COOP_VALUES as readonly string[]).includes(token)
    ? (token as CoopValue)
    : 'unsafe-none';
}

/**
 * Parse COOP response headers. Unknown directives fall back to
 * `unsafe-none` exactly as the structured-header parser would ignore them.
 */
export function parseCoop(headers: {
  readonly 'cross-origin-opener-policy'?: string | null;
  readonly 'cross-origin-opener-policy-report-only'?: string | null;
}): CoopPolicy {
  const value = parseDirective(headers['cross-origin-opener-policy']);
  const reportOnlyRaw = headers['cross-origin-opener-policy-report-only'];
  const reportOnly = reportOnlyRaw ? parseDirective(reportOnlyRaw) : null;
  const endpoints: string[] = [];
  for (const raw of [
    headers['cross-origin-opener-policy'],
    headers['cross-origin-opener-policy-report-only'],
  ]) {
    if (!raw) continue;
    for (const param of raw.split(';').slice(1)) {
      const match = /report-to\s*=\s*"?([^";]+)"?/i.exec(param);
      if (match) endpoints.push(match[1].trim());
    }
  }
  return { value, reportOnly, reportEndpoints: endpoints };
}

/**
 * Sandbox tokens that matter for opener/COOP behavior. A sandboxed document
 * without `allow-popups-to-escape-sandbox` is forced into `unsafe-none`
 * (it cannot isolate itself into a new BCG).
 */
export interface SandboxTokens {
  readonly sandboxed: boolean;
  readonly allowPopupsToEscapeSandbox: boolean;
  readonly allowSameOrigin: boolean;
}

export function parseSandbox(headerValue: string | null | undefined): SandboxTokens {
  if (headerValue === null || headerValue === undefined) {
    return {
      sandboxed: false,
      allowPopupsToEscapeSandbox: false,
      allowSameOrigin: false,
    };
  }
  const tokens = headerValue
    .split(/[\s;]+/)
    .map((token) => token.trim().toLowerCase())
    .filter(Boolean);
  return {
    sandboxed: true,
    allowPopupsToEscapeSandbox: tokens.includes('allow-popups-to-escape-sandbox'),
    allowSameOrigin: tokens.includes('allow-same-origin'),
  };
}

/**
 * Apply the sandbox override to a response COOP policy. A sandboxed browsing
 * context without `allow-popups-to-escape-sandbox` cannot place itself into a
 * fresh BCG, so its effective COOP is forced to `unsafe-none`. With the escape
 * flag (typically combined with `allow-same-origin`) the header is honored.
 */
export function effectiveCoop(policy: CoopPolicy, sandbox: SandboxTokens): CoopPolicy {
  if (sandbox.sandboxed && !sandbox.allowPopupsToEscapeSandbox) {
    return { ...policy, value: 'unsafe-none' };
  }
  return policy;
}

/** Resolve the effective document origin, applying the sandbox opaque rule. */
export function effectiveOrigin(
  responseOrigin: string | Origin,
  sandbox: SandboxTokens,
): Origin {
  const origin =
    typeof responseOrigin === 'string'
      ? parseOrigin(responseOrigin)
      : responseOrigin;
  if (sandbox.sandboxed && !sandbox.allowSameOrigin && !origin.opaque) {
    return opaqueOrigin(`sandbox:${origin.host}`);
  }
  return origin;
}

/**
 * HTML "check a navigation response's COOP" matching rule.
 *
 * Two policies match when their enums are identical and, for the two
 * restrictive enums, the navigated document is same-origin with the current
 * document. `same-origin-allow-popups` additionally has a popup-specific
 * virtual-policy exception handled in {@link matchPopupCoop}.
 */
export function coopMatchesForNavigation(
  current: CoopPolicy,
  response: CoopPolicy,
  currentOrigin: Origin,
  responseOrigin: Origin,
): boolean {
  if (current.value !== response.value) return false;
  if (current.value === 'unsafe-none') return true;
  return isSameOrigin(currentOrigin, responseOrigin);
}

/**
 * Popup COOP matching ("obtain a BCG for navigation response", popup branch).
 *
 * Beyond the ordinary match, an opener running `same-origin-allow-popups`
 * keeps a live opener reference when the popup itself is `unsafe-none` —
 * this is the intentional popup escape hatch and must not be confused with
 * the diagonal cases, which always sever the opener.
 */
export function matchPopupCoop(
  opener: CoopPolicy,
  popup: CoopPolicy,
  openerOrigin: Origin,
  popupOrigin: Origin,
): { matches: boolean; openerRetained: boolean } {
  const direct = coopMatchesForNavigation(opener, popup, openerOrigin, popupOrigin);
  if (direct) {
    return { matches: true, openerRetained: true };
  }
  const allowPopupEscape =
    opener.value === 'same-origin-allow-popups' && popup.value === 'unsafe-none';
  if (allowPopupEscape) {
    return { matches: true, openerRetained: true };
  }
  return { matches: false, openerRetained: false };
}

export type BcgSwitchReason =
  | 'coop-mismatch'
  | 'coop-cross-origin-restrictive'
  | 'noopener'
  | 'sandbox-force-none';

export interface NavigationDecision {
  /** A fresh BCG is allocated and the old one is left behind. */
  readonly switchesBrowsingContextGroup: boolean;
  /** For popup navigations: whether `window.opener` stays non-null. */
  readonly openerRetained: boolean;
  readonly reason: BcgSwitchReason | null;
  readonly currentCoop: CoopValue;
  readonly responseCoop: CoopValue;
}

export interface DocumentState {
  readonly origin: Origin;
  readonly coop: CoopPolicy;
  readonly sandbox: SandboxTokens;
  readonly browsingContextGroupId: string;
}

export interface NavigationInput {
  readonly responseOrigin: string | Origin;
  readonly coop?: CoopPolicy;
  readonly sandbox?: SandboxTokens;
  /** `rel=noopener`, `window.open(..., 'noopener=yes')`, etc. */
  readonly noopener?: boolean;
}

let bcgSequence = 0;

function nextBcgId(): string {
  bcgSequence += 1;
  return `bcg-${bcgSequence}`;
}

/** Test-only reset so repeated test runs get deterministic ids. */
export function resetBcgSequenceForTests(): void {
  bcgSequence = 0;
}

/**
 * Stateful BCG transition machine. Feed it the response description of each
 * top-level navigation / popup open and it tracks group membership and
 * opener links the way the browser process would.
 */
export class BrowsingContextGroupMachine {
  private current: DocumentState;
  private readonly openerState: DocumentState | null;
  readonly initialGroupId: string;

  constructor(initial: {
    readonly origin: string | Origin;
    readonly coop?: CoopPolicy;
    readonly sandbox?: SandboxTokens;
    /** Reuse an existing BCG id when reconstructing a context. */
    readonly browsingContextGroupId?: string;
  }) {
    const sandbox = initial.sandbox ?? parseSandbox(null);
    const coop = effectiveCoop(initial.coop ?? parseCoop({}), sandbox);
    const origin = effectiveOrigin(initial.origin, sandbox);
    const groupId = initial.browsingContextGroupId ?? nextBcgId();
    this.current = { origin, coop, sandbox, browsingContextGroupId: groupId };
    this.openerState = null;
    this.initialGroupId = groupId;
  }

  get state(): Readonly<DocumentState> {
    return this.current;
  }

  /**
   * Apply a top-level navigation in the current browsing context. A COOP
   * mismatch allocates a new BCG; a matching policy stays put.
   */
  navigate(input: NavigationInput): NavigationDecision {
    const sandbox = input.sandbox ?? parseSandbox(null);
    const responseCoop = effectiveCoop(input.coop ?? parseCoop({}), sandbox);
    const responseOrigin = effectiveOrigin(input.responseOrigin, sandbox);
    const previousCoop = this.current.coop.value;

    if (input.noopener) {
      const groupId = nextBcgId();
      this.current = {
        origin: responseOrigin,
        coop: responseCoop,
        sandbox,
        browsingContextGroupId: groupId,
      };
      return this.decision(true, false, 'noopener', previousCoop, responseCoop.value);
    }

    const matches = coopMatchesForNavigation(
      this.current.coop,
      responseCoop,
      this.current.origin,
      responseOrigin,
    );

    if (matches) {
      this.current = {
        ...this.current,
        origin: responseOrigin,
        coop: responseCoop,
        sandbox,
      };
      return this.decision(false, true, null, previousCoop, responseCoop.value);
    }

    const reason: BcgSwitchReason =
      this.current.coop.value !== responseCoop.value
        ? 'coop-mismatch'
        : 'coop-cross-origin-restrictive';
    const groupId = nextBcgId();
    this.current = {
      origin: responseOrigin,
      coop: responseCoop,
      sandbox,
      browsingContextGroupId: groupId,
    };
    return this.decision(true, false, reason, previousCoop, responseCoop.value);
  }

  /**
   * Model opening a popup. Returns the popup machine plus the decision of
   * whether the popup shares the opener's BCG and retains `window.opener`.
   */
  openPopup(input: NavigationInput): {
    popup: BrowsingContextGroupMachine;
    decision: NavigationDecision;
  } {
    const sandbox = input.sandbox ?? parseSandbox(null);
    const responseCoop = effectiveCoop(input.coop ?? parseCoop({}), sandbox);
    const responseOrigin = effectiveOrigin(input.responseOrigin, sandbox);

    if (input.noopener) {
      const popup = new BrowsingContextGroupMachine({
        origin: responseOrigin,
        coop: responseCoop,
        sandbox,
      });
      return {
        popup,
        decision: popup.decision(
          true,
          false,
          'noopener',
          this.current.coop.value,
          responseCoop.value,
        ),
      };
    }

    const { matches, openerRetained } = matchPopupCoop(
      this.current.coop,
      responseCoop,
      this.current.origin,
      responseOrigin,
    );

    if (matches) {
      const popup = BrowsingContextGroupMachine.joinGroup(
        { origin: responseOrigin, coop: responseCoop, sandbox },
        this.current.browsingContextGroupId,
      );
      return {
        popup,
        decision: popup.decision(
          false,
          openerRetained,
          null,
          this.current.coop.value,
          responseCoop.value,
        ),
      };
    }

    const reason: BcgSwitchReason =
      this.current.coop.value !== responseCoop.value
        ? 'coop-mismatch'
        : 'coop-cross-origin-restrictive';
    const popup = new BrowsingContextGroupMachine({
      origin: responseOrigin,
      coop: responseCoop,
      sandbox,
    });
    return {
      popup,
      decision: popup.decision(
        true,
        false,
        reason,
        this.current.coop.value,
        responseCoop.value,
      ),
    };
  }

  private static joinGroup(
    doc: { origin: Origin; coop: CoopPolicy; sandbox: SandboxTokens },
    groupId: string,
  ): BrowsingContextGroupMachine {
    const machine = new BrowsingContextGroupMachine({
      origin: doc.origin,
      coop: doc.coop,
      sandbox: doc.sandbox,
    });
    machine.current = { ...machine.current, browsingContextGroupId: groupId };
    return machine;
  }

  private decision(
    switches: boolean,
    openerRetained: boolean,
    reason: BcgSwitchReason | null,
    currentCoop: CoopValue,
    responseCoop: CoopValue,
  ): NavigationDecision {
    return {
      switchesBrowsingContextGroup: switches,
      openerRetained,
      reason,
      currentCoop,
      responseCoop,
    };
  }
}
