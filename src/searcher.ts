/**
 * Bounded XS-Leak / cross-origin-isolation breach searcher.
 *
 * Given a multi-origin deployment description, performs a breadth-first
 * search over navigation / popup / embedding / fetch actions and reports
 * the shortest action sequences that defeat a declared security goal.
 * All decisions are produced by the same COOP/COEP/CORP/CORS/metadata
 * engines the gateway uses online, so a finding always corresponds to a
 * concrete spec-level policy failure rather than a heuristic guess.
 */

import {
  BrowsingContextGroupMachine,
  parseCoop,
  parseSandbox,
  resetBcgSequenceForTests,
  type CoopPolicy,
  type NavigationDecision,
  type SandboxTokens,
} from './coop.ts';
import {
  evaluateCrossOriginIsolated,
  evaluateFrameEmbed,
  evaluateRedirectChain,
  evaluateSubresource,
  parseCoep,
  type CoepPolicy,
  type FrameEmbedVerdict,
} from './coep.ts';
import {
  validateCorsResponse,
  validatePreflight,
  type CorsResponseHeaders,
  type CorsVerdict,
} from './cors.ts';
import {
  evaluateFetchMetadata,
  type MetadataPolicyConfig,
  type MetadataVerdict,
} from './fetch-metadata.ts';
import {
  isSameOrigin,
  opaqueOrigin,
  parseOrigin,
  serializeOrigin,
  type Origin,
} from './origin.ts';

export type SecurityGoal =
  | 'cross-origin-isolated'
  | 'opener-protected'
  | 'credential-protected'
  | 'csrf-protected';

export type FindingSeverity = 'critical' | 'high' | 'medium' | 'low';

export interface CorsBehavior {
  /**
   * 'wildcard'  -> emits `Access-Control-Allow-Origin: *`
   * 'reflect'   -> echoes the request Origin after (mis)configured checks
   * 'allowlist' -> reflects only origins in `allowedOrigins`
   * 'none'      -> no CORS headers
   */
  readonly mode: 'wildcard' | 'reflect' | 'allowlist' | 'none';
  readonly allowCredentials?: boolean;
  /** reflect/allowlist mode: accept the literal opaque `null` origin. */
  readonly acceptNullOrigin?: boolean;
  readonly allowedOrigins?: readonly string[];
  readonly emitVaryOrigin?: boolean;
}

export interface EndpointConfig {
  /** Stable label used in action sequences, e.g. 'victim' / 'attacker'. */
  readonly id: string;
  readonly origin: string;
  readonly coop?: string | null;
  readonly coep?: string | null;
  readonly corp?: string | null;
  readonly sandbox?: string | null;
  readonly cors?: CorsBehavior;
  /** Fetch Metadata gate configuration protecting this endpoint. */
  readonly metadataGate?: MetadataPolicyConfig;
  /** Security goals the operator believes this endpoint satisfies. */
  readonly goals?: readonly SecurityGoal[];
}

export type ActionKind =
  | 'navigate'
  | 'open-popup'
  | 'embed-iframe'
  | 'load-subresource'
  | 'cors-fetch'
  | 'preflight'
  | 'redirect-fetch'
  | 'cross-site-post';

export interface SearchAction {
  readonly kind: ActionKind;
  readonly actorContext: string;
  readonly target: string;
  readonly mode?: 'no-cors' | 'cors';
  readonly credentials?: boolean;
  readonly requestOrigin?: string;
  readonly sandboxed?: boolean;
  readonly redirectChain?: readonly string[];
}

export interface SearchFinding {
  readonly id: string;
  readonly title: string;
  readonly severity: FindingSeverity;
  readonly violatedGoal: SecurityGoal;
  readonly sequence: readonly SearchAction[];
  readonly evidence:
    | NavigationDecision
    | FrameEmbedVerdict
    | CorsVerdict
    | MetadataVerdict
    | ReturnType<typeof evaluateSubresource>
    | ReturnType<typeof evaluateRedirectChain>
    | { crossOriginIsolated: boolean; reasons: readonly string[] };
  readonly explanation: string;
  readonly specReferences: readonly string[];
}

export interface SearchBounds {
  /** Maximum action sequence length. */
  readonly maxDepth?: number;
  /** Hard cap on expanded states to keep the search bounded. */
  readonly maxStates?: number;
  /** Cap on reported findings per category. */
  readonly maxFindingsPerKind?: number;
}

export interface SearchResult {
  readonly findings: readonly SearchFinding[];
  readonly statesExpanded: number;
  readonly statesPruned: number;
  readonly depthReached: number;
}

interface ContextRecord {
  id: string;
  endpointId: string | null;
  origin: Origin;
  coop: CoopPolicy;
  coep: CoepPolicy;
  sandbox: SandboxTokens;
  bcgId: string;
  openerContextId: string | null;
  parentContextId: string | null;
}

interface SearchState {
  contexts: Map<string, ContextRecord>;
  sequence: SearchAction[];
}

let contextSequence = 0;
let findingSequence = 0;

function nextContextId(): string {
  contextSequence += 1;
  return `ctx-${contextSequence}`;
}

function coopOf(endpoint: EndpointConfig): CoopPolicy {
  return parseCoop({
    'cross-origin-opener-policy': endpoint.coop ?? null,
  });
}

function coepOf(endpoint: EndpointConfig): CoepPolicy {
  return parseCoep({
    'cross-origin-embedder-policy': endpoint.coep ?? null,
  });
}

function serializeState(state: SearchState): string {
  const records = [...state.contexts.values()].map((context) => ({
    id: context.id,
    ep: context.endpointId,
    origin: serializeOrigin(context.origin),
    coop: context.coop.value,
    coep: context.coep.value,
    bcg: context.bcgId,
    opener: context.openerContextId,
    parent: context.parentContextId,
    sandbox: context.sandbox.sandboxed,
  }));
  return JSON.stringify(records);
}

function cloneState(state: SearchState): SearchState {
  return {
    contexts: new Map(
      [...state.contexts].map(([id, context]) => [id, { ...context }]),
    ),
    sequence: [...state.sequence],
  };
}

function buildFinding(
  title: string,
  severity: FindingSeverity,
  violatedGoal: SecurityGoal,
  sequence: readonly SearchAction[],
  evidence: SearchFinding['evidence'],
  explanation: string,
  specReferences: readonly string[],
): SearchFinding {
  findingSequence += 1;
  return {
    id: `finding-${findingSequence}`,
    title,
    severity,
    violatedGoal,
    sequence,
    evidence,
    explanation,
    specReferences,
  };
}

function corsResponseFor(
  endpoint: EndpointConfig,
  requestOrigin: string,
): CorsResponseHeaders {
  const behavior = endpoint.cors;
  if (!behavior || behavior.mode === 'none') return {};
  if (behavior.mode === 'wildcard') {
    return {
      'access-control-allow-origin': '*',
      'access-control-allow-credentials': behavior.allowCredentials
        ? 'true'
        : undefined,
    };
  }
  const isNull = requestOrigin.trim().toLowerCase() === 'null';
  if (isNull && !behavior.acceptNullOrigin) return {};
  let reflected: string | null = null;
  if (behavior.mode === 'reflect') {
    reflected = isNull ? 'null' : requestOrigin;
  } else if (behavior.mode === 'allowlist') {
    const allowed = new Set(behavior.allowedOrigins ?? []);
    if (!isNull && allowed.has(requestOrigin)) reflected = requestOrigin;
    if (isNull && behavior.acceptNullOrigin) reflected = 'null';
  }
  if (reflected === null) return {};
  return {
    'access-control-allow-origin': reflected,
    'access-control-allow-credentials': behavior.allowCredentials
      ? 'true'
      : undefined,
    vary: behavior.emitVaryOrigin === false ? undefined : 'Origin',
  };
}

const DEFAULT_BOUNDS: Required<SearchBounds> = {
  maxDepth: 3,
  maxStates: 2000,
  maxFindingsPerKind: 4,
};

const STATE_CHANGING_ACTIONS: ReadonlySet<ActionKind> = new Set([
  'navigate',
  'open-popup',
  'embed-iframe',
]);

/**
 * Bounded BFS entry point. Returns every distinct policy breach reachable
 * within {@link SearchBounds}, each attached to the shortest action
 * sequence that demonstrates it.
 */
export function searchIsolationBreaches(
  endpoints: readonly EndpointConfig[],
  bounds: SearchBounds = {},
): SearchResult {
  const effectiveBounds = { ...DEFAULT_BOUNDS, ...bounds };
  resetBcgSequenceForTests();
  contextSequence = 0;
  findingSequence = 0;

  const initial: SearchState = { contexts: new Map(), sequence: [] };
  for (const endpoint of endpoints) {
    const sandbox = parseSandbox(endpoint.sandbox ?? null);
    const origin = parseOrigin(endpoint.origin);
    const machine = new BrowsingContextGroupMachine({
      origin,
      coop: coopOf(endpoint),
      sandbox,
    });
    initial.contexts.set(machine.state.browsingContextGroupId, {
      id: nextContextId(),
      endpointId: endpoint.id,
      origin: machine.state.origin,
      coop: machine.state.coop,
      coep: coepOf(endpoint),
      sandbox: machine.state.sandbox,
      bcgId: machine.state.browsingContextGroupId,
      openerContextId: null,
      parentContextId: null,
    });
  }

  const visited = new Set<string>([serializeState(initial)]);
  const queue: Array<{ state: SearchState; depth: number }> = [
    { state: initial, depth: 0 },
  ];
  const findings: SearchFinding[] = [];
  const findingKeys = new Set<string>();
  let statesExpanded = 0;
  let statesPruned = 0;
  let depthReached = 0;

  const record = (finding: SearchFinding): void => {
    const key = `${finding.violatedGoal}:${finding.title}:${finding.sequence
      .map((action) => `${action.kind}>${action.target}`)
      .join('|')}`;
    if (findingKeys.has(key)) return;
    const sameKind = findings.filter(
      (existing) =>
        existing.violatedGoal === finding.violatedGoal &&
        existing.title === finding.title,
    ).length;
    if (sameKind >= effectiveBounds.maxFindingsPerKind) return;
    findingKeys.add(key);
    findings.push(finding);
  };

  while (queue.length > 0) {
    const entry = queue.shift()!;
    if (statesExpanded >= effectiveBounds.maxStates) break;
    statesExpanded += 1;
    depthReached = Math.max(depthReached, entry.depth);

    evaluateStaticChecks(entry.state, endpoints, record);
    if (entry.depth >= effectiveBounds.maxDepth) continue;

    for (const source of entry.state.contexts.values()) {
      for (const target of endpoints) {
        for (const action of candidateActions(source, target, endpoints)) {
          const next = applyAction(
            entry.state,
            source,
            target,
            action,
            endpoints,
            record,
          );
          if (!next) {
            statesPruned += 1;
            continue;
          }
          const key = serializeState(next);
          if (visited.has(key)) {
            statesPruned += 1;
            continue;
          }
          visited.add(key);
          queue.push({ state: next, depth: entry.depth + 1 });
        }
      }
    }
  }

  return {
    findings: orderFindings(findings),
    statesExpanded,
    statesPruned,
    depthReached,
  };
}

function candidateActions(
  source: ContextRecord,
  target: EndpointConfig,
  endpoints: readonly EndpointConfig[],
): SearchAction[] {
  if (source.endpointId === target.id) {
    return [
      { kind: 'navigate', actorContext: source.id, target: target.id },
    ];
  }
  const actions: SearchAction[] = [
    { kind: 'navigate', actorContext: source.id, target: target.id },
    { kind: 'open-popup', actorContext: source.id, target: target.id },
    {
      kind: 'embed-iframe',
      actorContext: source.id,
      target: target.id,
      sandboxed: target.sandbox !== null && target.sandbox !== undefined,
    },
    {
      kind: 'load-subresource',
      actorContext: source.id,
      target: target.id,
      mode: 'no-cors',
      credentials: true,
    },
    {
      kind: 'cors-fetch',
      actorContext: source.id,
      target: target.id,
      mode: 'cors',
      credentials: true,
    },
    {
      kind: 'preflight',
      actorContext: source.id,
      target: target.id,
      mode: 'cors',
      credentials: true,
    },
    {
      kind: 'cross-site-post',
      actorContext: source.id,
      target: target.id,
      requestOrigin:
        endpoints.find((candidate) => candidate.id === source.endpointId)
          ?.origin ?? serializeOrigin(source.origin),
    },
  ];
  if (target.sandbox) {
    actions.push({
      kind: 'redirect-fetch',
      actorContext: source.id,
      target: target.id,
      mode: 'cors',
      credentials: true,
      sandboxed: true,
    });
  }
  return actions;
}

function applyAction(
  state: SearchState,
  source: ContextRecord,
  target: EndpointConfig,
  action: SearchAction,
  endpoints: readonly EndpointConfig[],
  record: (finding: SearchFinding) => void,
): SearchState | null {
  const next = cloneState(state);
  next.sequence = [...state.sequence, action];
  const targetSandbox = parseSandbox(target.sandbox ?? null);

  if (action.kind === 'navigate') {
    const machine = machineFromContext(source);
    const decision = machine.navigate({
      responseOrigin: target.origin,
      coop: coopOf(target),
      sandbox: targetSandbox,
    });
    replaceContext(next, source, machine, target);
    return next;
  }

  if (action.kind === 'open-popup') {
    const openerMachine = machineFromContext(source);
    const { popup, decision } = openerMachine.openPopup({
      responseOrigin: target.origin,
      coop: coopOf(target),
      sandbox: targetSandbox,
    });
    detectOpenerLeak(next.sequence, target, decision, record);
    next.contexts.set(popup.state.browsingContextGroupId, {
      id: nextContextId(),
      endpointId: target.id,
      origin: popup.state.origin,
      coop: popup.state.coop,
      coep: coepOf(target),
      sandbox: popup.state.sandbox,
      bcgId: popup.state.browsingContextGroupId,
      openerContextId: decision.openerRetained ? source.id : null,
      parentContextId: null,
    });
    return next;
  }

  if (action.kind === 'embed-iframe') {
    const verdict = evaluateFrameEmbed({
      parentOrigin: source.origin,
      parentCoep: source.coep,
      childOrigin: target.origin,
      childCoep: coepOf(target),
      childCorp: target.corp ?? null,
      childSandbox: targetSandbox,
    });
    detectEmbedLeak(next.sequence, source, target, verdict, endpoints, record);
    if (!verdict.allowed) return null;
    next.contexts.set(`${source.bcgId}:frame:${target.id}`, {
      id: nextContextId(),
      endpointId: target.id,
      origin: targetSandbox.sandboxed && !targetSandbox.allowSameOrigin
        ? opaqueOrigin(`sandbox:${target.id}`)
        : parseOrigin(target.origin),
      coop: coopOf(target),
      coep: coepOf(target),
      sandbox: targetSandbox,
      bcgId: source.bcgId,
      openerContextId: null,
      parentContextId: source.id,
    });
    return next;
  }

  // Stateless actions do not grow the context graph; evaluate immediately.
  evaluateStatelessAction(source, target, action, next.sequence, record);
  return null;
}

function machineFromContext(context: ContextRecord): BrowsingContextGroupMachine {
  const machine = new BrowsingContextGroupMachine({
    origin: context.origin,
    coop: context.coop,
    sandbox: context.sandbox,
    browsingContextGroupId: context.bcgId,
  });
  return machine;
}

function replaceContext(
  state: SearchState,
  previous: ContextRecord,
  machine: BrowsingContextGroupMachine,
  target: EndpointConfig,
): void {
  state.contexts.delete(previous.bcgId);
  state.contexts.set(machine.state.browsingContextGroupId, {
    id: previous.id,
    endpointId: target.id,
    origin: machine.state.origin,
    coop: machine.state.coop,
    coep: coepOf(target),
    sandbox: machine.state.sandbox,
    bcgId: machine.state.browsingContextGroupId,
    openerContextId: previous.openerContextId,
    parentContextId: previous.parentContextId,
  });
}

function detectOpenerLeak(
  sequence: readonly SearchAction[],
  target: EndpointConfig,
  decision: NavigationDecision,
  record: (finding: SearchFinding) => void,
): void {
  const goal: SecurityGoal = 'opener-protected';
  if (!(target.goals ?? []).includes(goal)) return;
  if (decision.openerRetained && decision.responseCoop === 'unsafe-none') {
    record(
      buildFinding(
        'Live window.opener retained into cross-origin document',
        'high',
        goal,
        sequence,
        decision,
        'Target declares opener protection but answers without COOP, so an ' +
          'attacker-controlled popup keeps a live opener reference and can ' +
          'run XS-Leak timing/history probes against the victim window.',
        [
          'HTML §cross-origin-opener-policy',
          'HTML §obtain-a-browsing-context-group-for-navigation-response',
        ],
      ),
    );
  }
}

function detectEmbedLeak(
  sequence: readonly SearchAction[],
  source: ContextRecord,
  target: EndpointConfig,
  verdict: FrameEmbedVerdict,
  endpoints: readonly EndpointConfig[],
  record: (finding: SearchFinding) => void,
): void {
  const isolationGoal: SecurityGoal = 'cross-origin-isolated';
  const sourceEndpoint = endpoints.find(
    (candidate) => candidate.id === source.endpointId,
  );
  const parentClaimsIsolation =
    (sourceEndpoint?.goals ?? []).includes(isolationGoal) ||
    source.coep.value !== 'unsafe-none';

  if (
    verdict.allowed &&
    !verdict.childCrossOriginIsolated &&
    parentClaimsIsolation &&
    !isSameOrigin(source.origin, parseOrigin(target.origin))
  ) {
    record(
      buildFinding(
        'Cross-origin frame loads without joining the isolated agent cluster',
        'high',
        isolationGoal,
        sequence,
        verdict,
        'The frame loads but is not cross-origin-isolated (missing COEP or ' +
          'credentialless anonymous embedding), so Spectre-class shared memory ' +
          'guarantees do not apply to the nested document.',
        ['HTML §coop-coep-integration', 'HTML §check-a-navigation-response-COEP'],
      ),
    );
  }

}

function evaluateStaticChecks(
  state: SearchState,
  endpoints: readonly EndpointConfig[],
  record: (finding: SearchFinding) => void,
): void {
  for (const endpoint of endpoints) {
    const goals = new Set(endpoint.goals ?? []);
    if (!goals.has('cross-origin-isolated')) continue;
    const coop = coopOf(endpoint);
    const coep = coepOf(endpoint);
    const verdict = evaluateCrossOriginIsolated({
      origin: endpoint.origin,
      coop,
      coep,
      isTopLevel: true,
    });
    if (!verdict.crossOriginIsolated) {
      record(
        buildFinding(
          'Top-level document cannot reach crossOriginIsolated',
          'high',
          'cross-origin-isolated',
          [
            {
              kind: 'navigate',
              actorContext: endpoint.id,
              target: endpoint.id,
            },
          ],
          verdict,
          `crossOriginIsolated is false: ${verdict.reasons.join('; ')}`,
          ['HTML §coop-coep-integration', 'HTML §concept-cross-origin-isolation'],
        ),
      );
    }
  }

  // COOP diagonal matrix: cross-origin navigation that keeps one BCG because
  // both documents rely on unsafe-none (the opener-protected goal failure).
  for (const context of state.contexts.values()) {
    if (!context.endpointId) continue;
    const sourceEndpoint = endpoints.find(
      (candidate) => candidate.id === context.endpointId,
    );
    if (!sourceEndpoint) continue;
    for (const target of endpoints) {
      if (target.id === sourceEndpoint.id) continue;
      if (!(target.goals ?? []).includes('opener-protected')) continue;
      if (
        context.coop.value === 'unsafe-none' &&
        coopOf(target).value === 'unsafe-none'
      ) {
        record(
          buildFinding(
            'Cross-origin navigation stays in one BCG (no COOP on either side)',
            'medium',
            'opener-protected',
            [
              {
                kind: 'open-popup',
                actorContext: sourceEndpoint.id,
                target: target.id,
              },
            ],
            {
              switchesBrowsingContextGroup: false,
              openerRetained: true,
              reason: null,
              currentCoop: 'unsafe-none',
              responseCoop: 'unsafe-none',
            },
            'Neither document sends a restrictive COOP, so the popup shares ' +
              'the opener BCG and window.opener remains live across origins.',
            ['HTML §cross-origin-opener-policies'],
          ),
        );
      }
    }
  }
}

const SEVERITY_WEIGHT: Record<FindingSeverity, number> = {
  critical: 0,
  high: 1,
  medium: 2,
  low: 3,
};

function orderFindings(findings: SearchFinding[]): SearchFinding[] {
  return [...findings].sort((a, b) => {
    const severity = SEVERITY_WEIGHT[a.severity] - SEVERITY_WEIGHT[b.severity];
    if (severity !== 0) return severity;
    const length = a.sequence.length - b.sequence.length;
    if (length !== 0) return length;
    return a.id.localeCompare(b.id);
  });
}

function evaluateStatelessAction(
  source: ContextRecord,
  target: EndpointConfig,
  action: SearchAction,
  sequence: readonly SearchAction[],
  record: (finding: SearchFinding) => void,
): void {
  const actorOrigin =
    action.requestOrigin ?? serializeOrigin(source.origin);

  if (action.kind === 'load-subresource') {
    const verdict = evaluateSubresource(
      {
        embedderOrigin: source.origin,
        requestOrigin: target.origin,
        mode: 'no-cors',
        destination: 'script',
        credentials: action.credentials ? 'include' : 'omit',
        hops: [
          {
            url: target.origin,
            corp: target.corp ?? null,
            'access-control-allow-origin':
              corsResponseFor(target, actorOrigin)['access-control-allow-origin'] ??
              null,
          },
        ],
      },
      source.coep,
    );
    if (
      verdict.allowed &&
      source.coep.value !== 'unsafe-none' &&
      !isSameOrigin(source.origin, parseOrigin(target.origin)) &&
      !target.corp &&
      source.coep.value === 'credentialless'
    ) {
      record(
        buildFinding(
          'Credential-stripped cross-origin resource exposed without CORP',
          'medium',
          'cross-origin-isolated',
          sequence,
          verdict,
          'credentialless anonymously embeds a cross-origin response that ' +
            'declares no CORP; acceptable for public media but a finding when ' +
            'the response was intended to be credential-gated.',
          ['HTML §credentialless', 'Fetch §corp-check'],
        ),
      );
    }
    return;
  }

  if (action.kind === 'cors-fetch' || action.kind === 'preflight') {
    const headers = corsResponseFor(target, actorOrigin);
    const policy = {
      allowedOrigins: target.cors?.allowedOrigins ?? [],
      allowCredentials: target.cors?.allowCredentials ?? false,
      allowOpaqueNull: target.cors?.acceptNullOrigin ?? false,
    };
    const verdict =
      action.kind === 'preflight'
        ? validatePreflight(actorOrigin, headers, policy, ['authorization'])
        : validateCorsResponse(actorOrigin, headers, policy);
    detectCorsLeak(sequence, target, actorOrigin, verdict, action.kind, record);
    return;
  }

  if (action.kind === 'redirect-fetch') {
    const nullHeaders = corsResponseFor(target, 'null');
    const nullAccepted =
      nullHeaders['access-control-allow-origin']?.toLowerCase() === 'null' &&
      (nullHeaders['access-control-allow-credentials'] ?? '') === 'true';
    const verdict = evaluateRedirectChain(
      {
        embedderOrigin: source.origin,
        requestOrigin: target.origin,
        mode: 'cors',
        destination: 'document',
        credentials: 'include',
        hops: [
          { url: target.origin },
          {
            url: opaqueOrigin(`sandbox:${target.id}`),
            'access-control-allow-origin':
              nullHeaders['access-control-allow-origin'] ?? null,
          },
        ],
      },
      source.coep,
    );
    if (nullAccepted || verdict.allowed) {
      record(
        buildFinding(
          'Sandbox redirect with degenerated Origin: null accepted',
          'critical',
          'credential-protected',
          sequence,
          verdict,
          'A sandboxed iframe redirect degenerates Origin to null and the ' +
            'gateway still honors credentialed CORS, letting the sandboxed ' +
            'context read cross-origin response bodies.',
          [
            'Fetch §cors-protocol-and-credentials',
            'HTML §sandboxing-flag-set',
            'RFC 6454 §4 (serializing opaque origins as "null")',
          ],
        ),
      );
    }
    return;
  }

  if (action.kind === 'cross-site-post') {
    if (!target.metadataGate) return;
    const verdict = evaluateFetchMetadata(
      {
        method: 'POST',
        headers: {
          'sec-fetch-site': 'cross-site',
          'sec-fetch-mode': 'cors',
          'sec-fetch-dest': 'empty',
          origin: actorOrigin,
        },
      },
      target.metadataGate,
    );
    if (
      verdict.allow &&
      (target.goals ?? []).includes('csrf-protected')
    ) {
      record(
        buildFinding(
          'Cross-site state-changing POST passes the metadata gate',
          'critical',
          'csrf-protected',
          sequence,
          verdict,
          'Sec-Fetch-Site: cross-site POST is allowed, so a forged cross-site ' +
            'form/fetch can mutate state with ambient cookies (CSRF).',
          ['Fetch Metadata Request Headers W3C TAG draft', 'OWASP CSRF prevention'],
        ),
      );
    }
  }
}

function detectCorsLeak(
  sequence: readonly SearchAction[],
  target: EndpointConfig,
  actorOrigin: string,
  verdict: CorsVerdict,
  kind: 'cors-fetch' | 'preflight',
  record: (finding: SearchFinding) => void,
): void {
  if (!(target.goals ?? []).includes('credential-protected')) return;
  const wildcard = verdict.violations.some(
    (violation) =>
      violation.code === 'wildcard-with-credentials' ||
      violation.code === 'wildcard-cannot-credential',
  );
  if (wildcard) {
    record(
      buildFinding(
        'Credentialed wildcard CORS exposure',
        'critical',
        'credential-protected',
        sequence,
        verdict,
          `${kind} combines Access-Control-Allow-Origin:* with credentialed ` +
            'access; any attacker origin can read authenticated response bodies.',
        ['Fetch §cors-protocol-and-credentials'],
      ),
    );
  }
  const nullReflection = verdict.violations.some(
    (violation) => violation.code === 'opaque-null-reflection',
  );
  if (nullReflection) {
    record(
      buildFinding(
        'Opaque null Origin reflected with credentials',
        'critical',
        'credential-protected',
        sequence,
        verdict,
          `Reflecting Origin ${actorOrigin} as null grants sandboxed/redirect ` +
            'contexts credentialed cross-origin reads.',
        ['RFC 6454 §4', 'Fetch §cors-check'],
      ),
    );
  }
  const unnormalized = verdict.violations.some(
    (violation) =>
      violation.code === 'reflected-origin-unnormalized' ||
      violation.code === 'origin-not-allowlisted',
  );
  if (unnormalized) {
    record(
      buildFinding(
        'Reflected Origin accepted without normalized tuple comparison',
        'high',
        'credential-protected',
        sequence,
        verdict,
        'Raw Origin string reflection passes suffix/prefix lookalike origins ' +
          'such as https://victim.example.attacker.test.',
        ['URL §origin', 'Fetch §cors-protocol'],
      ),
    );
  }
}
