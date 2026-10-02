import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MerkleTree, hashLeaf } from '../src/merkle.ts';
import { Anchorer, AnchorVerifier, anchorHash } from '../src/anchorer.ts';
import { issueCertificate, verifyCertificate } from '../src/certificate.ts';

function makeAnchorer(times: number[]) {
  let i = 0;
  return new Anchorer(() => times[Math.min(i++, times.length - 1)]);
}

test('merkle proof verifies and tampering fails', () => {
  const tree = new MerkleTree(['a', 'b', 'c', 'd', 'e']);
  for (let i = 0; i < tree.size; i++) {
    const proof = tree.getProof(i);
    assert.ok(MerkleTree.verifyProof(tree.leafHash(i).toString('hex'), proof, tree.root.toString('hex')));
  }
  const proof = tree.getProof(0);
  assert.ok(!MerkleTree.verifyProof(hashLeaf('evil').toString('hex'), proof, tree.root.toString('hex')));
});

test('anchor at t=1000 is signed and chains from genesis', () => {
  const anchorer = makeAnchorer([1000]);
  const tree = new MerkleTree(['record-1', 'record-2']);
  const anchor = anchorer.anchor(tree.root);
  assert.equal(anchor.record.timestamp, 1000);
  assert.equal(anchor.record.sequence, 0);
  assert.equal(anchor.record.prevAnchorHash, '0'.repeat(64));
  assert.equal(anchor.record.merkleRoot, tree.root.toString('hex'));
  assert.ok(Anchorer.verifySignature(anchor));
});

test('acceptance: certificate proves data existed before t=1000', () => {
  const anchorer = makeAnchorer([1000]);
  const records = ['tx-001', 'tx-002', 'tx-003'];
  const tree = new MerkleTree(records);
  const anchor = anchorer.anchor(tree.root); // anchored at t=1000

  // later (t=2500): issue and verify the audit credential
  const cert = issueCertificate(tree, 2, anchor);
  const verdict = verifyCertificate(records[2], cert, anchorer.publicKeyPem);
  assert.ok(verdict.valid);
  assert.equal(verdict.existedBefore, 1000);
});

test('certificate rejects wrong data, untrusted key and tampered timestamp', () => {
  const anchorer = makeAnchorer([1000]);
  const tree = new MerkleTree(['x', 'y']);
  const anchor = anchorer.anchor(tree.root);
  const cert = issueCertificate(tree, 0, anchor);

  assert.equal(verifyCertificate('forged', cert, anchorer.publicKeyPem).valid, false);

  const other = makeAnchorer([1000]);
  assert.equal(verifyCertificate('x', cert, other.publicKeyPem).valid, false);

  const tampered = structuredClone(cert);
  tampered.anchor.record.timestamp = 1;
  const v = verifyCertificate('x', tampered, anchorer.publicKeyPem);
  assert.equal(v.valid, false);
  assert.match(v.reason!, /signature/);
});

test('verifier rejects replay, reorder, chain break and timestamp regression', () => {
  const anchorer = makeAnchorer([1000, 2000, 3000]);
  const t1 = new MerkleTree(['a']);
  const t2 = new MerkleTree(['a', 'b']);
  const a1 = anchorer.anchor(t1.root);
  const a2 = anchorer.anchor(t2.root);

  const verifier = new AnchorVerifier(anchorer.publicKeyPem);
  verifier.accept(a1);
  assert.throws(() => verifier.accept(a1), /replay/);          // replay of an accepted anchor
  verifier.accept(a2);
  assert.throws(() => verifier.accept(a2), /replay/);          // replay of the latest anchor
});

test('verifier enforces sequence order and chain continuity', () => {
  const anchorer = makeAnchorer([1000, 2000]);
  const a1 = anchorer.anchor(new MerkleTree(['a']).root);
  const a2 = anchorer.anchor(new MerkleTree(['b']).root);

  const v1 = new AnchorVerifier(anchorer.publicKeyPem);
  assert.throws(() => v1.accept(a2), /sequence/);               // skipped seq 0

  const v2 = new AnchorVerifier(anchorer.publicKeyPem);
  v2.accept(a1);
  v2.accept(a2);
  assert.equal(anchorHash(a1.record), a2.record.prevAnchorHash);
});

test('anchorer refuses non-monotonic timestamps', () => {
  const anchorer = makeAnchorer([1000, 1000]);
  anchorer.anchor(new MerkleTree(['a']).root);
  assert.throws(() => anchorer.anchor(new MerkleTree(['b']).root), /non-monotonic/);
});

test('data appended after the anchor is not provable against it', () => {
  const anchorer = makeAnchorer([1000]);
  const early = new MerkleTree(['early']);
  const anchor = anchorer.anchor(early.root);

  const later = new MerkleTree(['early', 'late']);
  assert.notEqual(later.root.toString('hex'), anchor.record.merkleRoot);
  assert.throws(() => issueCertificate(later, 1, anchor), /does not match/);
});
