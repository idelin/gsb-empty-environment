import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  allowsAnonymousCors,
  validateCorsResponse,
  validatePreflight,
} from '../src/cors.ts';

const VICTIM = 'https://victim.example';
const ATTACKER = 'https://attacker.test';

function policy(overrides = {}) {
  return {
    allowedOrigins: [ATTACKER],
    allowCredentials: true,
    ...overrides,
  };
}

test('wildcard plus credentials is a hard violation', () => {
  const verdict = validateCorsResponse(
    ATTACKER,
    {
      'access-control-allow-origin': '*',
      'access-control-allow-credentials': 'true',
    },
    policy(),
  );
  assert.equal(verdict.allowed, false);
  assert.equal(verdict.effectiveAllowOrigin, null);
  assert.ok(
    verdict.violations.some(
      (violation) => violation.code === 'wildcard-with-credentials',
    ),
  );
});

test('anonymous wildcard without credentials remains allowed', () => {
  const verdict = validateCorsResponse(
    ATTACKER,
    { 'access-control-allow-origin': '*' },
    policy({ allowCredentials: false }),
  );
  assert.equal(verdict.allowed, true);
  assert.equal(verdict.credentialsAllowed, false);
  assert.equal(verdict.effectiveAllowOrigin, '*');
});

test('normalized allowlist reflection with Vary passes', () => {
  const verdict = validateCorsResponse(
    ATTACKER,
    {
      'access-control-allow-origin': 'https://attacker.test:443',
      'access-control-allow-credentials': 'true',
      vary: 'Accept-Encoding, Origin',
    },
    policy(),
  );
  assert.equal(verdict.allowed, true);
  assert.equal(verdict.credentialsAllowed, true);
});

test('suffix lookalike origins fail tuple comparison', () => {
  const verdict = validateCorsResponse(
    'https://victim.example.attacker.test',
    {
      'access-control-allow-origin':
        'https://victim.example.attacker.test',
      'access-control-allow-credentials': 'true',
      vary: 'Origin',
    },
    { allowedOrigins: [VICTIM], allowCredentials: true },
  );
  assert.equal(verdict.allowed, false);
  assert.ok(
    verdict.violations.some(
      (violation) => violation.code === 'origin-not-allowlisted',
    ),
  );
});

test('reflected Origin without Vary: Origin is flagged', () => {
  const verdict = validateCorsResponse(
    ATTACKER,
    {
      'access-control-allow-origin': ATTACKER,
      'access-control-allow-credentials': 'true',
    },
    policy(),
  );
  assert.ok(
    verdict.violations.some(
      (violation) => violation.code === 'missing-vary-origin',
    ),
  );
});

test('literal null reflection is refused for credentialed responses', () => {
  const verdict = validateCorsResponse(
    'null',
    {
      'access-control-allow-origin': 'null',
      'access-control-allow-credentials': 'true',
    },
    policy(),
  );
  assert.equal(verdict.allowed, false);
  assert.ok(
    verdict.violations.some(
      (violation) => violation.code === 'opaque-null-reflection',
    ),
  );
});

test('explicit opt-in still allows null only when policy permits it', () => {
  const verdict = validateCorsResponse(
    'null',
    { 'access-control-allow-origin': 'null' },
    { allowedOrigins: [], allowOpaqueNull: true },
  );
  assert.equal(verdict.allowed, false);
  assert.equal(
    verdict.violations.every(
      (violation) => violation.code !== 'opaque-null-reflection',
    ),
    true,
  );
});

test('request Origin null with mismatched reflected origin is blocked', () => {
  const verdict = validateCorsResponse(
    'null',
    {
      'access-control-allow-origin': ATTACKER,
      'access-control-allow-credentials': 'true',
      vary: 'Origin',
    },
    policy(),
  );
  assert.equal(verdict.allowed, false);
  assert.ok(
    verdict.violations.some(
      (violation) => violation.code === 'opaque-null-reflection',
    ),
  );
});

test('preflight wildcard headers cannot cover Authorization credentialed', () => {
  const verdict = validatePreflight(
    ATTACKER,
    {
      'access-control-allow-origin': ATTACKER,
      'access-control-allow-credentials': 'true',
      'access-control-allow-headers': '*',
      vary: 'Origin',
    },
    policy(),
    ['Authorization'],
  );
  assert.equal(verdict.allowed, false);
  assert.ok(
    verdict.violations.some(
      (violation) =>
        violation.code === 'preflight-wildcard-headers-with-credentials',
    ),
  );
});

test('allowsAnonymousCors accepts wildcard and exact origin only', () => {
  assert.equal(allowsAnonymousCors(ATTACKER, '*'), true);
  assert.equal(allowsAnonymousCors(ATTACKER, ATTACKER), true);
  assert.equal(allowsAnonymousCors(ATTACKER, VICTIM), false);
  assert.equal(allowsAnonymousCors(ATTACKER, 'null'), false);
  assert.equal(allowsAnonymousCors(ATTACKER, null), false);
});

test('unparseable ACAO yields an invalid-header violation', () => {
  const verdict = validateCorsResponse(
    ATTACKER,
    { 'access-control-allow-origin': 'not-an-origin' },
    policy(),
  );
  assert.equal(verdict.allowed, false);
  assert.ok(
    verdict.violations.some(
      (violation) => violation.code === 'invalid-header-value',
    ),
  );
});
