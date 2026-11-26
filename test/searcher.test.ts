import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  matrixSummary,
  runScenarioMatrix,
  searchIsolationBreaches,
  type EndpointConfig,
} from '../src/index.ts';

const VICTIM: EndpointConfig = {
  id: 'victim',
  origin: 'https://victim.example',
  coop: 'same-origin',
  coep: 'require-corp',
  goals: [
    'cross-origin-isolated',
    'opener-protected',
    'credential-protected',
    'csrf-protected',
  ],
};

const ATTACKER: EndpointConfig = {
  id: 'attacker',
  origin: 'https://attacker.test',
  cors: { mode: 'wildcard', allowCredentials: true },
  sandbox: 'allow-scripts',
  metadataGate: { serverOrigin: 'https://victim.example' },
  goals: ['credential-protected', 'csrf-protected'],
};

test('scenario matrix has every cell passing', () => {
  const summary = matrixSummary(runScenarioMatrix());
  assert.equal(summary.failed.length, 0, JSON.stringify(summary.failed));
  assert.equal(summary.total >= 38, true);
  for (const category of Object.keys(summary.byCategory)) {
    const bucket = summary.byCategory[category];
    assert.equal(bucket.passed, bucket.total, category);
  }
});

test('finds the credentialed wildcard CORS exposure with a shortest sequence', () => {
  const result = searchIsolationBreaches([VICTIM, ATTACKER], { maxDepth: 2 });
  const wildcard = result.findings.filter((finding) =>
    finding.title.includes('wildcard CORS'),
  );
  assert.ok(wildcard.length > 0);
  assert.equal(wildcard[0].severity, 'critical');
  assert.equal(wildcard[0].sequence.length, 1);
  assert.equal(wildcard[0].sequence[0].kind, 'cors-fetch');
});

test('finds CSRF gap when the metadata gate allows cross-site POST', () => {
  const permissiveAttacker: EndpointConfig = {
    ...ATTACKER,
    metadataGate: {
      serverOrigin: ATTACKER.origin,
      // Misconfigured broad allow rule that also admits state-changing POSTs.
      crossSiteAllowlist: [{ dest: '*', methods: ['GET', 'POST'] }],
    },
  };
  const result = searchIsolationBreaches([VICTIM, permissiveAttacker], {
    maxDepth: 1,
  });
  const csrf = result.findings.find(
    (finding) => finding.violatedGoal === 'csrf-protected',
  );
  assert.ok(csrf);
  assert.equal(csrf?.severity, 'critical');
  assert.equal(csrf?.sequence[0].kind, 'cross-site-post');
});

test('detects live window.opener when COOP is absent on both sides', () => {
  const weakVictim: EndpointConfig = {
    ...VICTIM,
    coop: null,
    goals: ['opener-protected'],
  };
  const weakAttacker: EndpointConfig = { id: 'attacker', origin: ATTACKER.origin };
  const result = searchIsolationBreaches([weakVictim, weakAttacker], {
    maxDepth: 2,
  });
  const opener = result.findings.find(
    (finding) =>
      finding.violatedGoal === 'opener-protected' &&
      finding.sequence.some((step) => step.kind === 'open-popup'),
  );
  assert.ok(opener, 'expected an opener-protected finding');
});

test('flags a top-level document that cannot reach crossOriginIsolated', () => {
  const broken: EndpointConfig = {
    id: 'broken',
    origin: 'https://broken.example',
    coop: 'same-origin-allow-popups',
    coep: 'require-corp',
    goals: ['cross-origin-isolated'],
  };
  const result = searchIsolationBreaches([broken], { maxDepth: 1 });
  const isolation = result.findings.find(
    (finding) => finding.title.includes('crossOriginIsolated'),
  );
  assert.ok(isolation);
  assert.equal(isolation?.violatedGoal, 'cross-origin-isolated');
});

test('hardened deployment produces no credential or CSRF findings', () => {
  const hardenedAttacker: EndpointConfig = {
    id: 'attacker',
    origin: ATTACKER.origin,
    corp: 'cross-origin',
    coep: 'require-corp',
    coop: 'same-origin',
    cors: {
      mode: 'allowlist',
      allowCredentials: true,
      allowedOrigins: ['https://victim.example'],
    },
    metadataGate: { serverOrigin: 'https://victim.example' },
    goals: ['credential-protected', 'csrf-protected'],
  };
  const result = searchIsolationBreaches([VICTIM, hardenedAttacker], {
    maxDepth: 2,
  });
  const critical = result.findings.filter(
    (finding) => finding.severity === 'critical',
  );
  assert.deepEqual(critical, []);
});

test('sandbox null-origin redirect is reported when the endpoint accepts null', () => {
  const nullAccepting: EndpointConfig = {
    id: 'sandboxed',
    origin: 'https://sandbox.example',
    sandbox: 'allow-scripts',
    cors: {
      mode: 'reflect',
      allowCredentials: true,
      acceptNullOrigin: true,
    },
    goals: ['credential-protected'],
  };
  const result = searchIsolationBreaches([VICTIM, nullAccepting], {
    maxDepth: 1,
  });
  const nullFinding = result.findings.find(
    (finding) => finding.title.includes('degenerated Origin: null'),
  );
  assert.ok(nullFinding);
  assert.equal(nullFinding?.severity, 'critical');
});

test('search stays bounded and reports expansion statistics', () => {
  const result = searchIsolationBreaches([VICTIM, ATTACKER], {
    maxDepth: 2,
    maxStates: 20,
  });
  assert.ok(result.statesExpanded <= 20);
  assert.ok(result.depthReached <= 2);
  assert.ok(result.findings.length > 0);
});

test('findings are ordered by severity then shortest sequence', () => {
  const result = searchIsolationBreaches([VICTIM, ATTACKER], { maxDepth: 2 });
  const weights = { critical: 0, high: 1, medium: 2, low: 3 } as const;
  for (let index = 1; index < result.findings.length; index += 1) {
    const previous = result.findings[index - 1];
    const current = result.findings[index];
    assert.ok(
      weights[previous.severity] <= weights[current.severity],
    );
  }
});
