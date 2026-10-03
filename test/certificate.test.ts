import { test } from "node:test";
import assert from "node:assert/strict";
import { MerkleTree } from "../src/merkle.ts";
import {
  commitTrajectory,
  selectWitnesses,
  verifyCertificate,
} from "../src/certificate.ts";
import { SpacetimeGridIndex } from "../src/grid_index.ts";
import { IntersectionProver, verifyProof } from "../src/prover.ts";
import { sha256Hex } from "../src/hash.ts";
import type { TrajectoryPoint } from "../src/types.ts";

function pt(t: number, x: number, y: number = 0): TrajectoryPoint {
  return { t, x, y, z: 0 };
}

test("Merkle 树：包含证明往返验证，篡改叶子失败", () => {
  const leaves = ["a", "b", "c", "d", "e"].map((s) => sha256Hex(s));
  const tree = new MerkleTree(leaves);
  for (let i = 0; i < leaves.length; i++) {
    assert.ok(MerkleTree.verify(leaves[i], tree.proof(i), tree.root));
  }
  assert.ok(!MerkleTree.verify(sha256Hex("x"), tree.proof(0), tree.root));
  assert.ok(!MerkleTree.verify(leaves[0], tree.proof(1), tree.root));
});

test("轨迹承诺：见证点覆盖目标时段且 Merkle 证明有效", () => {
  const traj = Array.from({ length: 20 }, (_, i) => pt(i * 1000, i));
  const committed = commitTrajectory("E", traj);
  const witnesses = selectWitnesses(committed, 5000, 9000);
  const times = witnesses.map((w) => w.t);
  assert.deepEqual(times, [4000, 5000, 6000, 7000, 8000, 9000, 10000]);
  for (const w of witnesses) {
    assert.ok(MerkleTree.verify(w.hash, w.merkleProof, committed.commitment.merkleRoot));
  }
});

function buildProof() {
  const index = new SpacetimeGridIndex({ sliceDurationMs: 60_000, cellSizeMeters: 10 });
  index.insert("A", [pt(0, -50), pt(100_000, 50)]);
  index.insert("B", [pt(0, 0), pt(100_000, 0)]);
  const prover = new IntersectionProver(index);
  return prover.prove("A", "B", 10);
}

test("证书链：完整证明通过独立验证", () => {
  const proof = buildProof();
  assert.equal(proof.intervals.length, 1);
  const result = verifyProof(proof);
  assert.ok(result.ok, result.errors.join("; "));
  assert.equal(result.certificateResults.length, 1);
});

test("不可抵赖：篡改最近距离后验证失败", () => {
  const proof = buildProof();
  proof.certificates[0].minDistanceMeters = 0.0001;
  const result = verifyProof(proof);
  assert.equal(result.ok, false);
});

test("不可抵赖：篡改见证轨迹点后验证失败", () => {
  const proof = buildProof();
  proof.certificates[0].witnesses[0].points[0].x += 100;
  const result = verifyProof(proof);
  assert.equal(result.ok, false);
});

test("不可抵赖：替换承诺 Merkle 根后验证失败", () => {
  const proof = buildProof();
  proof.certificates[0].commitmentA.merkleRoot = sha256Hex("forged");
  const result = verifyProof(proof);
  assert.equal(result.ok, false);
});

test("不可抵赖：证书链断裂（调换证书顺序）后验证失败", () => {
  const index = new SpacetimeGridIndex({ sliceDurationMs: 60_000, cellSizeMeters: 10 });
  // 构造两段相遇：A 两次经过静止的 B。
  index.insert("A", [pt(0, -100), pt(100_000, 100), pt(200_000, -100)]);
  index.insert("B", [pt(0, 0), pt(200_000, 0)]);
  const proof = new IntersectionProver(index).prove("A", "B", 15);
  assert.equal(proof.certificates.length, 2);
  const [c0, c1] = proof.certificates;
  proof.certificates = [c1, c0];
  const result = verifyProof(proof);
  assert.equal(result.ok, false);
});

test("verifyCertificate 直接校验单张证书", () => {
  const proof = buildProof();
  const result = verifyCertificate(proof.certificates[0]);
  assert.ok(result.ok, result.errors.join("; "));
});
