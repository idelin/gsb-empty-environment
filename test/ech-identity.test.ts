import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  EchIdentityVerifier,
  certificateMatchesName,
  type EchHandshakeOutcome,
} from '../src/ech-identity.ts';

const INNER = 'secret.example.com';
const PUBLIC = 'front.cdn.example';

const innerCert = { dnsNames: [INNER] };
const publicCert = { dnsNames: [PUBLIC] };
const unrelatedCert = { dnsNames: ['attacker.example'] };
const retryConfigs = new Uint8Array([0xfe, 0x0d, 1, 2, 3]);

function verifier(): EchIdentityVerifier {
  return new EchIdentityVerifier({
    innerServerName: INNER,
    publicName: PUBLIC,
  });
}

function rejected(
  certificate: EchHandshakeOutcome['certificate'],
  configs?: Uint8Array,
): EchHandshakeOutcome {
  return { echAccepted: false, certificate, retryConfigs: configs };
}

test('certificateMatchesName: exact, case-insensitive, single-label wildcard', () => {
  assert.equal(certificateMatchesName(innerCert, INNER), true);
  assert.equal(certificateMatchesName(innerCert, INNER.toUpperCase()), true);
  const wild = { dnsNames: ['*.example.com'] };
  assert.equal(certificateMatchesName(wild, 'a.example.com'), true);
  assert.equal(certificateMatchesName(wild, 'example.com'), false);
  assert.equal(certificateMatchesName(wild, 'a.b.example.com'), false);
  assert.equal(certificateMatchesName({ dnsNames: [] }, INNER), false);
});

test('ECH accepted with a certificate valid for the inner SNI proceeds', () => {
  const v = verifier();
  const decision = v.handleHandshakeResult({
    echAccepted: true,
    certificate: innerCert,
  });
  assert.deepEqual(decision, { action: 'proceed' });
  assert.doesNotThrow(() => v.assertApplicationDataAllowed());
});

test('ECH accepted with only a public_name certificate aborts (impersonation)', () => {
  const v = verifier();
  const decision = v.handleHandshakeResult({
    echAccepted: true,
    certificate: publicCert,
  });
  assert.equal(decision.action, 'abort');
  assert.throws(() => v.assertApplicationDataAllowed());
});

test('ECH rejected with a public_name certificate adopts retry_configs, no app data', () => {
  const v = verifier();
  const decision = v.handleHandshakeResult(rejected(publicCert, retryConfigs));
  assert.deepEqual(decision, {
    action: 'retry',
    retryConfigs,
    allowApplicationData: false,
  });
  assert.throws(() => v.assertApplicationDataAllowed());
});

test('ECH rejected with an inner-name certificate aborts and discards retry_configs', () => {
  const v = verifier();
  const decision = v.handleHandshakeResult(rejected(innerCert, retryConfigs));
  assert.equal(decision.action, 'abort');
  assert.match(
    decision.action === 'abort' ? decision.reason : '',
    /public_name/,
  );
  assert.throws(() => v.assertApplicationDataAllowed());
});

test('ECH rejected with an unrelated certificate aborts', () => {
  const v = verifier();
  const decision = v.handleHandshakeResult(
    rejected(unrelatedCert, retryConfigs),
  );
  assert.equal(decision.action, 'abort');
});

test('a second rejection aborts: at most one retry', () => {
  const v = verifier();
  const first = v.handleHandshakeResult(rejected(publicCert, retryConfigs));
  assert.equal(first.action, 'retry');
  const second = v.handleHandshakeResult(rejected(publicCert, retryConfigs));
  assert.equal(second.action, 'abort');
  assert.match(
    second.action === 'abort' ? second.reason : '',
    /retry limit/,
  );
});

test('rejection without retry_configs never falls back to non-ECH', () => {
  const v = verifier();
  const decision = v.handleHandshakeResult(rejected(publicCert, undefined));
  assert.equal(decision.action, 'abort');
  assert.match(
    decision.action === 'abort' ? decision.reason : '',
    /without ECH/,
  );
  const empty = verifier();
  const emptyDecision = empty.handleHandshakeResult(
    rejected(publicCert, new Uint8Array(0)),
  );
  assert.equal(emptyDecision.action, 'abort');
});

test('full retry flow: reject then accept proceeds exactly once', () => {
  const v = verifier();
  assert.equal(
    v.handleHandshakeResult(rejected(publicCert, retryConfigs)).action,
    'retry',
  );
  const retried = v.handleHandshakeResult({
    echAccepted: true,
    certificate: innerCert,
  });
  assert.deepEqual(retried, { action: 'proceed' });
  assert.doesNotThrow(() => v.assertApplicationDataAllowed());
});
