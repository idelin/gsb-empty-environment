import { test } from 'node:test';
import assert from 'node:assert/strict';
import { verifyPdf } from '../src/index.ts';
import {
  buildSignedContract,
  buildWithDocTimestamp,
  buildWithDss,
  buildHideAttack,
  buildReplaceAttack,
  buildHideAndReplaceAttack,
  buildAnnotationTamper,
  buildFontReplaceAttack,
  buildOrphanObjectAttack,
} from './fixtures.ts';

test('accepts a legal document timestamp added in a later revision', () => {
  const baseline = buildSignedContract();
  const report = verifyPdf(buildWithDocTimestamp(baseline));
  assert.equal(report.valid, true, JSON.stringify(report.findings));
  assert.equal(report.revisionCount, 2);
  const types = report.signatures.map((sig) => sig.type).sort();
  assert.deepEqual(types, ['doc-timestamp', 'signature']);
});

test('accepts a legal /DSS revocation-information revision', () => {
  const report = verifyPdf(buildWithDss(buildSignedContract()));
  assert.equal(report.valid, true, JSON.stringify(report.findings));
});

test('blocks a Hide attack that removes the signed page from /Kids', () => {
  const report = verifyPdf(buildHideAttack(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code.startsWith('SHADOW_')));
});

test('blocks a Replace attack swapping the page /Contents stream', () => {
  const report = verifyPdf(buildReplaceAttack(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'SHADOW_PAGE_MODIFIED'));
});

test('blocks a Hide-and-Replace attack swapping the trailer /Root', () => {
  const report = verifyPdf(buildHideAndReplaceAttack(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'SHADOW_ROOT_SWAP'));
});

test('blocks tampering with the signed widget annotation geometry', () => {
  const report = verifyPdf(buildAnnotationTamper(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'SHADOW_ANNOTATION_MODIFIED'));
});

test('blocks a font/resource replacement that changes rendering', () => {
  const report = verifyPdf(buildFontReplaceAttack(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(
    report.findings.some(
      (f) => f.code === 'SHADOW_RESOURCE_MODIFIED' || f.code === 'SHADOW_FONT_MODIFIED',
    ),
  );
});

test('blocks an orphan object added outside the signature/DSS whitelist', () => {
  const report = verifyPdf(buildOrphanObjectAttack(buildSignedContract()));
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'UNAPPROVED_NEW_REACHABLE'));
});

test('every blocked attack keeps the original signature cryptographically structured', () => {
  const attacks = [
    buildHideAttack,
    buildReplaceAttack,
    buildHideAndReplaceAttack,
    buildAnnotationTamper,
    buildFontReplaceAttack,
    buildOrphanObjectAttack,
  ];
  for (const build of attacks) {
    const report = verifyPdf(build(buildSignedContract()));
    const original = report.signatures.find((sig) => sig.objectNumber === 9);
    assert.ok(original, 'original signature must still be located');
    assert.equal(original?.byteRangeValid, true);
    assert.equal(report.valid, false);
  }
});

test('audits the original signature independently after a legal timestamp', () => {
  const baseline = buildSignedContract();
  const report = verifyPdf(buildWithDocTimestamp(baseline));
  const approval = report.signatures.find((sig) => sig.type === 'signature');
  const timestamp = report.signatures.find((sig) => sig.type === 'doc-timestamp');
  assert.ok(approval && timestamp);
  assert.equal(approval?.byteRangeValid, true);
  assert.equal(timestamp?.byteRangeValid, true);
  assert.equal(approval?.signingRevisionIndex, 0);
  assert.equal(timestamp?.signingRevisionIndex, 1);
});

test('a shadow attack after a timestamp still fails the combined report', () => {
  const baseline = buildSignedContract();
  const stamped = buildWithDocTimestamp(baseline);
  const tampered = buildReplaceAttack({
    bytes: stamped,
    revisionEnd: baseline.revisionEnd,
    objectNumbers: baseline.objectNumbers,
  });
  const report = verifyPdf(tampered);
  assert.equal(report.valid, false);
  assert.ok(report.findings.some((f) => f.code === 'SHADOW_PAGE_MODIFIED'));
});
