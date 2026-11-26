/**
 * Normative scenario matrix: every meaningful COOP / COEP / CORP / CORS /
 * Fetch-Metadata combination with its spec-mandated outcome.
 *
 * The matrix is executable — `runScenarioMatrix()` evaluates every cell with
 * the real engines and returns per-cell pass/fail evidence, which makes the
 * package self-verifying against regressions and documents the expected
 * browser/server behavior in one place.
 */

import {
  BrowsingContextGroupMachine,
  coopMatchesForNavigation,
  effectiveCoop,
  matchPopupCoop,
  parseCoop,
  parseSandbox,
} from './coop.ts';
import {
  corpAllows,
  evaluateCrossOriginIsolated,
  evaluateFrameEmbed,
  evaluateRedirectChain,
  evaluateSubresource,
  parseCoep,
  parseCorp,
} from './coep.ts';
import { validateCorsResponse } from './cors.ts';
import { evaluateFetchMetadata } from './fetch-metadata.ts';
import { opaqueOrigin, parseOrigin } from './origin.ts';

export type MatrixCategory =
  | 'COOP'
  | 'COOP-COEP-isolation'
  | 'COEP-CORP-subresource'
  | 'COEP-frame'
  | 'CORS'
  | 'Fetch-Metadata'
  | 'Redirect-null';

export interface MatrixCell {
  readonly id: string;
  readonly category: MatrixCategory;
  readonly scenario: string;
  readonly expected: unknown;
  readonly specReference: string;
}

export interface MatrixResult extends MatrixCell {
  readonly actual: unknown;
  readonly pass: boolean;
}

const VICTIM = 'https://victim.example';
const ATTACKER = 'https://attacker.test';

function coop(value: string | null) {
  return parseCoop({ 'cross-origin-opener-policy': value });
}

function coep(value: string | null) {
  return parseCoep({ 'cross-origin-embedder-policy': value });
}

interface EvaluatedCell extends MatrixCell {
  evaluate: () => unknown;
}

const cells: EvaluatedCell[] = [
  {
    id: 'COOP-01',
    category: 'COOP',
    scenario: 'unsafe-none -> unsafe-none same-origin stays in BCG',
    expected: false,
    specReference: 'HTML §check-a-navigation-response-COOP',
    evaluate: () => {
      const machine = new BrowsingContextGroupMachine({
        origin: VICTIM,
        coop: coop('unsafe-none'),
      });
      return machine.navigate({
        responseOrigin: VICTIM,
        coop: coop('unsafe-none'),
      }).switchesBrowsingContextGroup;
    },
  },
  {
    id: 'COOP-02',
    category: 'COOP',
    scenario: 'same-origin -> same-origin cross-origin switches BCG',
    expected: true,
    specReference: 'HTML §check-a-navigation-response-COOP',
    evaluate: () =>
      !coopMatchesForNavigation(
        coop('same-origin'),
        coop('same-origin'),
        parseOrigin(VICTIM),
        parseOrigin(ATTACKER),
      ),
  },
  {
    id: 'COOP-03',
    category: 'COOP',
    scenario: 'same-origin -> same-origin same-origin stays in BCG',
    expected: true,
    specReference: 'HTML §check-a-navigation-response-COOP',
    evaluate: () =>
      coopMatchesForNavigation(
        coop('same-origin'),
        coop('same-origin'),
        parseOrigin(VICTIM),
        parseOrigin(VICTIM),
      ),
  },
  {
    id: 'COOP-04',
    category: 'COOP',
    scenario: 'allow-popups opener retains unsafe-none popup',
    expected: true,
    specReference: 'HTML §obtain-bcg-popup-virtual-policy',
    evaluate: () =>
      matchPopupCoop(
        coop('same-origin-allow-popups'),
        coop('unsafe-none'),
        parseOrigin(VICTIM),
        parseOrigin(ATTACKER),
      ).openerRetained,
  },
  {
    id: 'COOP-05',
    category: 'COOP',
    scenario: 'same-origin opener severs opener for unsafe-none popup',
    expected: false,
    specReference: 'HTML §obtain-bcg-popup-virtual-policy',
    evaluate: () =>
      matchPopupCoop(
        coop('same-origin'),
        coop('unsafe-none'),
        parseOrigin(VICTIM),
        parseOrigin(ATTACKER),
      ).openerRetained,
  },
  {
    id: 'COOP-06',
    category: 'COOP',
    scenario: 'diagonal allow-popups -> same-origin severs opener',
    expected: false,
    specReference: 'HTML §cross-origin-opener-policies',
    evaluate: () =>
      matchPopupCoop(
        coop('same-origin-allow-popups'),
        coop('same-origin'),
        parseOrigin(VICTIM),
        parseOrigin(ATTACKER),
      ).openerRetained,
  },
  {
    id: 'COOP-07',
    category: 'COOP',
    scenario: 'sandboxed response forced to unsafe-none COOP',
    expected: 'unsafe-none',
    specReference: 'HTML §sandboxing-flag-set',
    evaluate: () =>
      effectiveCoop(coop('same-origin'), parseSandbox('allow-scripts')).value,
  },
  {
    id: 'COOP-08',
    category: 'COOP',
    scenario: 'noopener always allocates a fresh BCG',
    expected: true,
    specReference: 'HTML §window-open-steps',
    evaluate: () => {
      const machine = new BrowsingContextGroupMachine({
        origin: VICTIM,
        coop: coop('unsafe-none'),
      });
      return machine.navigate({
        responseOrigin: VICTIM,
        coop: coop('unsafe-none'),
        noopener: true,
      }).switchesBrowsingContextGroup;
    },
  },
  {
    id: 'ISO-01',
    category: 'COOP-COEP-isolation',
    scenario: 'COOP same-origin + COEP require-corp => isolated',
    expected: true,
    specReference: 'HTML §concept-cross-origin-isolation',
    evaluate: () =>
      evaluateCrossOriginIsolated({
        origin: VICTIM,
        coop: coop('same-origin'),
        coep: coep('require-corp'),
        isTopLevel: true,
      }).crossOriginIsolated,
  },
  {
    id: 'ISO-02',
    category: 'COOP-COEP-isolation',
    scenario: 'COOP same-origin + COEP credentialless => isolated',
    expected: true,
    specReference: 'HTML §credentialless',
    evaluate: () =>
      evaluateCrossOriginIsolated({
        origin: VICTIM,
        coop: coop('same-origin'),
        coep: coep('credentialless'),
        isTopLevel: true,
      }).crossOriginIsolated,
  },
  {
    id: 'ISO-03',
    category: 'COOP-COEP-isolation',
    scenario: 'COEP require-corp without COOP same-origin => not isolated',
    expected: false,
    specReference: 'HTML §concept-cross-origin-isolation',
    evaluate: () =>
      evaluateCrossOriginIsolated({
        origin: VICTIM,
        coop: coop('same-origin-allow-popups'),
        coep: coep('require-corp'),
        isTopLevel: true,
      }).crossOriginIsolated,
  },
  {
    id: 'ISO-04',
    category: 'COOP-COEP-isolation',
    scenario: 'nested require-corp child under isolated parent => isolated',
    expected: true,
    specReference: 'HTML §check-a-navigation-response-COEP',
    evaluate: () =>
      evaluateCrossOriginIsolated({
        origin: ATTACKER,
        coop: coop('same-origin'),
        coep: coep('require-corp'),
        isTopLevel: false,
        parentIsolated: true,
      }).crossOriginIsolated,
  },
  {
    id: 'CORP-01',
    category: 'COEP-CORP-subresource',
    scenario: 'require-corp blocks cross-origin no-cors response without CORP',
    expected: false,
    specReference: 'Fetch §cross-origin-embedder-policy-check',
    evaluate: () =>
      evaluateSubresource(
        {
          embedderOrigin: VICTIM,
          requestOrigin: ATTACKER,
          mode: 'no-cors',
          destination: 'script',
          hops: [{ url: ATTACKER }],
        },
        coep('require-corp'),
      ).allowed,
  },
  {
    id: 'CORP-02',
    category: 'COEP-CORP-subresource',
    scenario: 'require-corp allows CORP cross-origin no-cors response',
    expected: true,
    specReference: 'Fetch §corp-check',
    evaluate: () =>
      evaluateSubresource(
        {
          embedderOrigin: VICTIM,
          requestOrigin: ATTACKER,
          mode: 'no-cors',
          destination: 'image',
          hops: [{ url: ATTACKER, corp: 'cross-origin' }],
        },
        coep('require-corp'),
      ).allowed,
  },
  {
    id: 'CORP-03',
    category: 'COEP-CORP-subresource',
    scenario: 'CORP same-origin blocks cross-site no-cors response',
    expected: false,
    specReference: 'Fetch §corp-check',
    evaluate: () =>
      corpAllows(parseCorp('same-origin'), 'cross-site', 'no-cors'),
  },
  {
    id: 'CORP-04',
    category: 'COEP-CORP-subresource',
    scenario: 'CORP same-site allows subdomain no-cors response',
    expected: true,
    specReference: 'Fetch §corp-check',
    evaluate: () =>
      corpAllows(parseCorp('same-site'), 'same-site', 'no-cors'),
  },
  {
    id: 'CORP-05',
    category: 'COEP-CORP-subresource',
    scenario: 'require-corp allows cross-origin CORS with matching ACAO',
    expected: true,
    specReference: 'Fetch §cors-check + COEP',
    evaluate: () =>
      evaluateSubresource(
        {
          embedderOrigin: VICTIM,
          requestOrigin: ATTACKER,
          mode: 'cors',
          destination: 'font',
          hops: [
            { url: ATTACKER, 'access-control-allow-origin': VICTIM },
          ],
        },
        coep('require-corp'),
      ).allowed,
  },
  {
    id: 'CORP-06',
    category: 'COEP-CORP-subresource',
    scenario: 'require-corp blocks cross-origin CORS without ACAO',
    expected: false,
    specReference: 'Fetch §cors-check + COEP',
    evaluate: () =>
      evaluateSubresource(
        {
          embedderOrigin: VICTIM,
          requestOrigin: ATTACKER,
          mode: 'cors',
          destination: 'font',
          hops: [{ url: ATTACKER }],
        },
        coep('require-corp'),
      ).allowed,
  },
  {
    id: 'CORP-07',
    category: 'COEP-CORP-subresource',
    scenario: 'credentialless strips credentials and allows CORP-less media',
    expected: 'omit',
    specReference: 'HTML §credentialless',
    evaluate: () =>
      evaluateSubresource(
        {
          embedderOrigin: VICTIM,
          requestOrigin: ATTACKER,
          mode: 'no-cors',
          destination: 'image',
          credentials: 'include',
          hops: [{ url: ATTACKER }],
        },
        coep('credentialless'),
      ).effectiveCredentials,
  },
  {
    id: 'CORP-08',
    category: 'COEP-CORP-subresource',
    scenario: 'redirect chain fails on the second hop without CORP',
    expected: 1,
    specReference: 'Fetch §http-redirect-fetch',
    evaluate: () =>
      evaluateSubresource(
        {
          embedderOrigin: VICTIM,
          requestOrigin: ATTACKER,
          mode: 'no-cors',
          destination: 'script',
          hops: [
            { url: ATTACKER, corp: 'cross-origin' },
            { url: 'https://cdn.other.test' },
          ],
        },
        coep('require-corp'),
      ).failedHop,
  },
  {
    id: 'FRAME-01',
    category: 'COEP-frame',
    scenario: 'require-corp parent blocks COEP-less cross-origin iframe',
    expected: false,
    specReference: 'HTML §check-a-navigation-response-COEP',
    evaluate: () =>
      evaluateFrameEmbed({
        parentOrigin: VICTIM,
        parentCoep: coep('require-corp'),
        childOrigin: ATTACKER,
        childCoep: coep(null),
        childCorp: 'cross-origin',
      }).allowed,
  },
  {
    id: 'FRAME-02',
    category: 'COEP-frame',
    scenario: 'require-corp parent accepts require-corp cross-origin iframe',
    expected: true,
    specReference: 'HTML §check-a-navigation-response-COEP',
    evaluate: () =>
      evaluateFrameEmbed({
        parentOrigin: VICTIM,
        parentCoep: coep('require-corp'),
        childOrigin: ATTACKER,
        childCoep: coep('require-corp'),
        childCorp: 'cross-origin',
      }).childCrossOriginIsolated,
  },
  {
    id: 'FRAME-03',
    category: 'COEP-frame',
    scenario: 'credentialless parent embeds COEP-less iframe anonymously',
    expected: true,
    specReference: 'HTML §credentialless-iframe',
    evaluate: () =>
      evaluateFrameEmbed({
        parentOrigin: VICTIM,
        parentCoep: coep('credentialless'),
        childOrigin: ATTACKER,
        childCoep: coep(null),
      }).credentiallessEmbedded,
  },
  {
    id: 'FRAME-04',
    category: 'COEP-frame',
    scenario: 'same-origin COEP-less child loads but stays non-isolated',
    expected: false,
    specReference: 'HTML §coop-coep-integration',
    evaluate: () =>
      evaluateFrameEmbed({
        parentOrigin: VICTIM,
        parentCoep: coep('require-corp'),
        childOrigin: VICTIM,
        childCoep: coep(null),
      }).childCrossOriginIsolated,
  },
  {
    id: 'CORS-01',
    category: 'CORS',
    scenario: 'wildcard ACAO + credentials is rejected',
    expected: false,
    specReference: 'Fetch §cors-protocol-and-credentials',
    evaluate: () =>
      validateCorsResponse(
        ATTACKER,
        {
          'access-control-allow-origin': '*',
          'access-control-allow-credentials': 'true',
        },
        { allowedOrigins: [ATTACKER], allowCredentials: true },
      ).allowed,
  },
  {
    id: 'CORS-02',
    category: 'CORS',
    scenario: 'normalized allowlist reflection is accepted',
    expected: true,
    specReference: 'Fetch §cors-protocol',
    evaluate: () =>
      validateCorsResponse(
        ATTACKER,
        {
          'access-control-allow-origin': 'https://attacker.test:443',
          'access-control-allow-credentials': 'true',
          vary: 'Origin',
        },
        { allowedOrigins: [ATTACKER], allowCredentials: true },
      ).allowed,
  },
  {
    id: 'CORS-03',
    category: 'CORS',
    scenario: 'suffix lookalike origin is rejected by tuple comparison',
    expected: false,
    specReference: 'URL §origin',
    evaluate: () =>
      validateCorsResponse(
        'https://victim.example.attacker.test',
        {
          'access-control-allow-origin':
            'https://victim.example.attacker.test',
          'access-control-allow-credentials': 'true',
          vary: 'Origin',
        },
        { allowedOrigins: [VICTIM], allowCredentials: true },
      ).allowed,
  },
  {
    id: 'CORS-04',
    category: 'CORS',
    scenario: 'reflected Origin without Vary: Origin is flagged',
    expected: 'missing-vary-origin',
    specReference: 'RFC 9110 §12.5.5 (caching)',
    evaluate: () =>
      validateCorsResponse(
        ATTACKER,
        {
          'access-control-allow-origin': ATTACKER,
          'access-control-allow-credentials': 'true',
        },
        { allowedOrigins: [ATTACKER], allowCredentials: true },
      ).violations[0]?.code ?? null,
  },
  {
    id: 'CORS-05',
    category: 'CORS',
    scenario: 'literal null ACAO is refused by default',
    expected: false,
    specReference: 'RFC 6454 §4',
    evaluate: () =>
      validateCorsResponse(
        'null',
        {
          'access-control-allow-origin': 'null',
          'access-control-allow-credentials': 'true',
        },
        { allowedOrigins: [] },
      ).allowed,
  },
  {
    id: 'META-01',
    category: 'Fetch-Metadata',
    scenario: 'same-origin request is allowed',
    expected: true,
    specReference: 'Fetch Metadata §resource-isolation-policy',
    evaluate: () =>
      evaluateFetchMetadata(
        {
          method: 'POST',
          headers: {
            'sec-fetch-site': 'same-origin',
            'sec-fetch-mode': 'cors',
            'sec-fetch-dest': 'empty',
            origin: VICTIM,
          },
        },
        { serverOrigin: VICTIM },
      ).allow,
  },
  {
    id: 'META-02',
    category: 'Fetch-Metadata',
    scenario: 'cross-site POST is denied as CSRF',
    expected: 'deny-csrf-state-change',
    specReference: 'Fetch Metadata §resource-isolation-policy',
    evaluate: () =>
      evaluateFetchMetadata(
        {
          method: 'POST',
          headers: {
            'sec-fetch-site': 'cross-site',
            'sec-fetch-mode': 'cors',
            'sec-fetch-dest': 'empty',
            origin: ATTACKER,
          },
        },
        { serverOrigin: VICTIM },
      ).code,
  },
  {
    id: 'META-03',
    category: 'Fetch-Metadata',
    scenario: 'user-activated top-level GET navigation is allowed',
    expected: true,
    specReference: 'Fetch Metadata §resource-isolation-policy',
    evaluate: () =>
      evaluateFetchMetadata(
        {
          method: 'GET',
          headers: {
            'sec-fetch-site': 'cross-site',
            'sec-fetch-mode': 'navigate',
            'sec-fetch-dest': 'document',
            'sec-fetch-user': '?1',
          },
        },
        { serverOrigin: VICTIM },
      ).allow,
  },
  {
    id: 'META-04',
    category: 'Fetch-Metadata',
    scenario: 'cross-site image subresource probe is denied',
    expected: 'deny-cross-site-subresource',
    specReference: 'Fetch Metadata §resource-isolation-policy',
    evaluate: () =>
      evaluateFetchMetadata(
        {
          method: 'GET',
          headers: {
            'sec-fetch-site': 'cross-site',
            'sec-fetch-mode': 'no-cors',
            'sec-fetch-dest': 'image',
            origin: ATTACKER,
          },
        },
        { serverOrigin: VICTIM },
      ).code,
  },
  {
    id: 'META-05',
    category: 'Fetch-Metadata',
    scenario: 'Origin: null is denied even with same-origin claim',
    expected: 'deny-null-origin-privilege',
    specReference: 'RFC 6454 §4 + Fetch Metadata',
    evaluate: () =>
      evaluateFetchMetadata(
        {
          method: 'GET',
          headers: {
            'sec-fetch-site': 'same-origin',
            'sec-fetch-mode': 'no-cors',
            'sec-fetch-dest': 'iframe',
            origin: 'null',
          },
        },
        { serverOrigin: VICTIM },
      ).code,
  },
  {
    id: 'META-06',
    category: 'Fetch-Metadata',
    scenario: 'spoofed Sec-Fetch-Site contradicted by Origin is denied',
    expected: 'deny-malformed-metadata',
    specReference: 'Fetch Metadata §sec-fetch-site-header',
    evaluate: () =>
      evaluateFetchMetadata(
        {
          method: 'GET',
          headers: {
            'sec-fetch-site': 'same-origin',
            'sec-fetch-mode': 'cors',
            'sec-fetch-dest': 'empty',
            origin: ATTACKER,
          },
        },
        { serverOrigin: VICTIM },
      ).code,
  },
  {
    id: 'REDIRECT-01',
    category: 'Redirect-null',
    scenario:
      'sandbox redirect to opaque origin without ACAO blocks the CORS load',
    expected: 'cors-not-allowed',
    specReference: 'HTML §sandboxing + RFC 6454 §4',
    evaluate: () =>
      evaluateRedirectChain(
        {
          embedderOrigin: VICTIM,
          requestOrigin: ATTACKER,
          mode: 'cors',
          destination: 'document',
          hops: [
            { url: ATTACKER },
            { url: opaqueOrigin('sandbox:frame') },
          ],
        },
        coep('require-corp'),
      ).reason,
  },
  {
    id: 'REDIRECT-03',
    category: 'Redirect-null',
    scenario:
      'no-cors subresource hitting an opaque redirect hop is refused outright',
    expected: 'opaque-origin-redirect',
    specReference: 'HTML §sandboxing + RFC 6454 §4',
    evaluate: () =>
      evaluateRedirectChain(
        {
          embedderOrigin: VICTIM,
          requestOrigin: ATTACKER,
          mode: 'no-cors',
          destination: 'script',
          hops: [
            { url: ATTACKER, corp: 'cross-origin' },
            { url: opaqueOrigin('sandbox:frame') },
          ],
        },
        coep('require-corp'),
      ).reason,
  },
  {
    id: 'REDIRECT-02',
    category: 'Redirect-null',
    scenario: 'opaque hop with valid anonymous ACAO passes under CORS mode',
    expected: true,
    specReference: 'Fetch §cors-check',
    evaluate: () =>
      evaluateRedirectChain(
        {
          embedderOrigin: VICTIM,
          requestOrigin: ATTACKER,
          mode: 'cors',
          destination: 'document',
          hops: [
            {
              url: opaqueOrigin('sandbox:frame'),
              'access-control-allow-origin': VICTIM,
            },
          ],
        },
        coep('require-corp'),
      ).allowed,
  },
];

/** Evaluate every matrix cell and return the evidence list. */
export function runScenarioMatrix(): MatrixResult[] {
  return cells.map((cell) => {
    const actual = cell.evaluate();
    return {
      id: cell.id,
      category: cell.category,
      scenario: cell.scenario,
      expected: cell.expected,
      actual,
      pass: Object.is(actual, cell.expected),
      specReference: cell.specReference,
    };
  });
}

/** Summary grouped by category for CI gating and documentation generation. */
export function matrixSummary(results: MatrixResult[] = runScenarioMatrix()): {
  readonly total: number;
  readonly passed: number;
  readonly failed: readonly MatrixResult[];
  readonly byCategory: Readonly<Record<string, { total: number; passed: number }>>;
} {
  const byCategory: Record<string, { total: number; passed: number }> = {};
  for (const result of results) {
    const bucket = (byCategory[result.category] ??= { total: 0, passed: 0 });
    bucket.total += 1;
    if (result.pass) bucket.passed += 1;
  }
  return {
    total: results.length,
    passed: results.filter((result) => result.pass).length,
    failed: results.filter((result) => !result.pass),
    byCategory,
  };
}
