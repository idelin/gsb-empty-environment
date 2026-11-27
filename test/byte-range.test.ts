import { test } from 'node:test';
import assert from 'node:assert/strict';
import { verifyPdf } from '../src/index.ts';
import {
  buildSignedContract,
  buildByteRangeNotFromZero,
  buildByteRangeTruncated,
  buildByteRangeHiddenObjectInGap,
  buildByteRangeObjectTokenInGap,
  buildTailGarbage,
} from './fixtures.ts';

test('accepts a conforming signature covering the full revision through %%EOF', () => {
  const report = verifyPdf(buildSignedContract().bytes);
  assert.equal(report.valid, true);
  assert.equal(report.signatures.length, 1);
  const sig = report.signatures[0];
  assert.deepEqual(sig.byteRange, [0, 691, 824, 276]);
  assert.equal(sig.byteRangeValid, true);
  assert.equal(sig.signingRevisionIndex, 0);
  assert.deepEqual(sig.signedDataRanges, [[0, 691], [824, 1100]]);
  assert.equal(report.signedObjectCount, 9);
});

test('rejects a /ByteRange that does not start at offset zero', () => {
  const report = verifyPdf(buildByteRangeNotFromZero(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'BYTERANGE_NOT_FROM_ZERO'));
});

test('rejects a truncated second span that leaves the xref/trailer uncovered', () => {
  const report = verifyPdf(buildByteRangeTruncated(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'BYTERANGE_NO_SNAPSHOT'));
});

test('rejects an extra entry smuggled inside the unsigned gap', () => {
  const report = verifyPdf(buildByteRangeHiddenObjectInGap(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'BYTERANGE_GAP_INVALID'));
});

test('rejects an indirect-object token smuggled inside the unsigned gap', () => {
  const report = verifyPdf(buildByteRangeObjectTokenInGap(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(
    report.findings.some(
      (f) => f.code === 'BYTERANGE_HIDDEN_OBJECT' ||
        f.code === 'BYTERANGE_GAP_INVALID' ||
        f.code === 'STRUCTURE_PARSE_ERROR' ||
        f.code === 'NO_SIGNATURE',
    ),
  );
});

test('rejects non-whitespace tail garbage after the signing %%EOF', () => {
  const report = verifyPdf(buildTailGarbage(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'TAIL_GARBAGE'));
});

test('reports no signatures for an unsigned document', () => {
  const bytes = buildSignedContract().bytes;
  const neutered = bytes.slice();
  const replacements: Array<[string, string]> = [
    ['/FT /Sig', '/FT /Btn'],
    ['/Type /Sig ', '/Type /Xxx '],
  ];
  for (const [from, to] of replacements) {
    const needle = new TextEncoder().encode(from);
    const replacement = new TextEncoder().encode(to);
    assert.equal(needle.length, replacement.length);
    outer: for (let i = 0; i + needle.length <= neutered.length; i += 1) {
      for (let j = 0; j < needle.length; j += 1) {
        if (neutered[i + j] !== needle[j]) continue outer;
      }
      neutered.set(replacement, i);
    }
  }
  const report = verifyPdf(neutered);
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'NO_SIGNATURE'));
});
