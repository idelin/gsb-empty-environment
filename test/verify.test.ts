import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  certifyInclusion,
  compileBaseline,
  compileNewDeployment,
  RULE_VERSION,
  verifyCertificate,
  type Grammar,
} from '../src/index.ts';
import { asmRef, baselinePolicy, KEY_A, KEY_B } from './fixtures.ts';

function pair() {
  const policy = baselinePolicy();
  const old = compileBaseline(policy);
  const next = compileNewDeployment(policy);
  return { old, next, cert: certifyInclusion(next, old, 1024) };
}

test('a forged proof edge is rejected by the independent verifier', () => {
  const { old, next, cert } = pair();
  const forged = structuredClone(cert);
  forged.proof[0].matches[0].oldAltIndex = 99;
  assert.equal(verifyCertificate(forged, next, old).valid, false);
});

test('a certificate replayed against different grammars fails hash verification', () => {
  const { old, next, cert } = pair();
  const tampered = structuredClone(next);
  tampered.start = 'P';
  assert.equal(verifyCertificate(cert, tampered, old).valid, false);
  assert.equal(verifyCertificate(cert, next, tampered).valid, false);
});

test('a verdict flipped to subset without a complete proof is rejected', () => {
  const { old, next } = pair();
  const widened: Grammar = structuredClone(next);
  widened.productions[0].alts.push({
    assembly: { name: 'CoreLib', publicKey: KEY_B, versions: [{ min: '1.0.0.0', max: '1.0.0.0' }] },
    typeName: 'String',
  });
  const widenedCert = certifyInclusion(widened, old, 1024);
  assert.equal(widenedCert.verdict, 'widened');
  const flipped = structuredClone(widenedCert);
  flipped.verdict = 'subset';
  assert.equal(verifyCertificate(flipped, widened, old).valid, false);
});

test('contravariant positions require reversed containment orientation', () => {
  const old: Grammar = {
    ruleVersion: RULE_VERSION,
    start: 'F',
    productions: [
      {
        nt: 'F',
        alts: [
          {
            assembly: asmRef('1.0.0.0'),
            typeName: 'Func`1',
            args: [{ nt: 'X', variance: 'contravariant' }],
          },
        ],
      },
      { nt: 'X', alts: [{ assembly: asmRef('1.0.0.0'), typeName: 'String' }] },
    ],
  };
  const inner = structuredClone(old);
  const cert = certifyInclusion(inner, old, 1024);
  assert.equal(cert.verdict, 'subset');
  assert.equal(verifyCertificate(cert, inner, old).valid, true);

  const widened = structuredClone(old);
  widened.productions[1].alts.push({
    assembly: { name: 'CoreLib', publicKey: KEY_A, versions: [{ min: '2.0.0.0', max: '2.0.0.0' }] },
    typeName: 'String',
  });
  const widenedCert = certifyInclusion(widened, old, 1024);
  assert.equal(widenedCert.verdict, 'widened');
});
