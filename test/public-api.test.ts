import { test } from 'node:test';
import assert from 'node:assert/strict';
import { verifyPdf } from '../src/index.ts';
import { buildSignedContract } from './fixtures.ts';

test('returns a structured report instead of throwing on empty input', () => {
  const report = verifyPdf(new Uint8Array(0));
  assert.equal(report.valid, false);
  assert.equal(report.revisionCount, 0);
  assert.ok(report.findings.some((f) => f.code === 'STRUCTURE_PARSE_ERROR'));
});

test('returns a structured report on non-PDF garbage', () => {
  const report = verifyPdf(new TextEncoder().encode('not a pdf at all'));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'STRUCTURE_PARSE_ERROR'));
});

test('exposes signed byte spans and hex for an external crypto layer', () => {
  const bytes = buildSignedContract().bytes;
  const report = verifyPdf(bytes);
  const sig = report.signatures[0];
  assert.ok(sig);
  assert.equal(sig.signedDataRanges.length, 2);
  assert.equal(sig.contentsHex.length > 0, true);
  assert.equal(sig.cryptographic, 'not-verified');
});
