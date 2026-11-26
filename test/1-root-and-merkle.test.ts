import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  buildMerkleTree,
  decideCookieCarry,
  proveExclusion,
  proveInclusion,
  sha256,
  RulesVerifier,
  toHex,
  verifyMerkleProof,
  type TrustAnchor,
} from '../src/index.ts';
import { ancestorChain, buildHarness, cookie, httpOrigin, request, signedBundle } from './harness.ts';

test('Merkle 包含与缺失证明可被独立验证，篡改即失败', () => {
  const entries: Array<[string, string]> = [
    ['com', '1'],
    ['*.ck', '1'],
    ['!www.ck', '1'],
  ];
  const tree = buildMerkleTree(entries, sha256);
  const values = new Map(entries);
  const inclusion = proveInclusion(tree, values, '*.ck', sha256);
  assert.equal(verifyMerkleProof(inclusion, tree.root, sha256), true);

  const exclusion = proveExclusion(tree, values, 'net', sha256);
  assert.equal(verifyMerkleProof(exclusion, tree.root, sha256), true);

  const tampered = { ...inclusion, value: '2' };
  assert.equal(verifyMerkleProof(tampered, tree.root, sha256), false);

  const wrongOrder = { ...exclusion, insertIndex: 99 };
  assert.equal(verifyMerkleProof(wrongOrder, tree.root, sha256), false);

  // 空树缺失证明。
  const empty = buildMerkleTree([], sha256);
  const emptyExclusion = proveExclusion(empty, new Map(), 'anything', sha256);
  assert.equal(verifyMerkleProof(emptyExclusion, empty.root, sha256), true);
});

test('缺少带外 trustedRulesRoot 直接拒绝，且不产生任何放行', () => {
  const harness = buildHarness();
  const req = request({
    url: 'https://shop.example/pay',
    method: 'GET',
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
  });
  const cookies = [cookie({ name: 'sid', domain: 'example' })];

  const noAnchor = decideCookieCarry({
    request: req,
    cookies,
    trustAnchor: null,
    proofBundle: signedBundle(harness, ['shop.example'], 1000),
    nowMs: 2000,
  });
  assert.equal(noAnchor.rulesVerification.accepted, false);
  assert.equal(noAnchor.rulesVerification.reason, 'missing-external-root');
  assert.deepEqual(noAnchor.cookies.map((decision) => decision.send), [false]);
  assert.equal(noAnchor.cookies[0].reason, 'missing-external-root');

  const noBundle = decideCookieCarry({
    request: req,
    cookies,
    trustAnchor: harness.anchor,
    proofBundle: null,
    nowMs: 2000,
  });
  assert.equal(noBundle.rulesVerification.reason, 'missing-external-root');
  assert.deepEqual(noBundle.cookies.map((decision) => decision.send), [false]);
});

test('受签根签名不符或锚点不匹配时拒绝', () => {
  const harness = buildHarness();
  const other = buildHarness();
  const req = request({
    url: 'https://shop.example/pay',
    method: 'GET',
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
  });
  const cookies = [cookie({ name: 'sid', domain: 'example' })];

  const wrongAnchor: TrustAnchor = { ...harness.anchor, anchorId: 'anchor-other' };
  const decision = decideCookieCarry({
    request: req,
    cookies,
    trustAnchor: wrongAnchor,
    proofBundle: signedBundle(harness, ['shop.example'], 1000),
    nowMs: 2000,
  });
  assert.equal(decision.rulesVerification.reason, 'untrusted-rules-root');
  assert.deepEqual(decision.cookies.map((entry) => entry.send), [false]);

  // 用另一把私钥签发的根。
  const foreignBundle = signedBundle(other, ['shop.example'], 1000);
  const foreignDecision = decideCookieCarry({
    request: req,
    cookies,
    trustAnchor: harness.anchor,
    proofBundle: foreignBundle,
    nowMs: 2000,
  });
  assert.equal(foreignDecision.rulesVerification.reason, 'untrusted-rules-root');
});

test('证明束被篡改（叶子值翻转）时独立核验拒绝', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example'], 1000);
  const hostProof = bundle.hostProofs[0];
  const firstKey = Object.keys(hostProof.proofs)[0];
  const original = hostProof.proofs[firstKey];
  const tamperedProofs = {
    ...hostProof.proofs,
    [firstKey]:
      original.kind === 'inclusion'
        ? { ...original, value: '9' }
        : { ...original, insertIndex: original.insertIndex + 1 },
  };
  const tamperedBundle = {
    ...bundle,
    hostProofs: [{ ...hostProof, proofs: tamperedProofs }],
  };
  const verifier = new RulesVerifier(harness.anchor, sha256);
  const root = verifier.verifyRoot(bundle);
  assert.equal(root.ok, true);
  if (root.ok) {
    assert.throws(() => verifier.verifyHostProof(tamperedBundle, root.merkleRoot, 'shop.example'));
  }
  assert.equal(toHex(buildMerkleTree([], sha256).root).length, 64);
});
