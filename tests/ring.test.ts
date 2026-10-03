import { test } from "node:test";
import assert from "node:assert/strict";
import { ConsistentHashRing } from "../src/ring.ts";
import { encodeKey, md5_32 } from "../src/hashes.ts";
import { mulberry32 } from "../src/sampler.ts";

const nodes = (n: number) => Array.from({ length: n }, (_, i) => `node-${i}`);

test("ring positions are sorted and owners valid", () => {
  const ring = new ConsistentHashRing(nodes(8), 50, md5_32);
  assert.equal(ring.positions.length, 400);
  for (let i = 1; i < ring.positions.length; i++) {
    assert.ok(ring.positions[i] >= ring.positions[i - 1]);
  }
  for (const o of ring.owners) assert.ok(o >= 0 && o < 8);
});

test("every hash value maps to a node", () => {
  const ring = new ConsistentHashRing(nodes(4), 20, md5_32);
  const rand = mulberry32(1);
  for (let i = 0; i < 10_000; i++) {
    const h = Math.floor(rand() * 0x100000000);
    const owner = ring.lookup(h);
    assert.ok(owner >= 0 && owner < 4);
  }
});

test("single-node ring owns the whole hash space", () => {
  const ring = new ConsistentHashRing(nodes(1), 10, md5_32);
  assert.equal(ring.lookup(0), 0);
  assert.equal(ring.lookup(0xffffffff), 0);
});

test("lookup wraps around past the last point", () => {
  const ring = new ConsistentHashRing(nodes(3), 30, md5_32);
  const h = ring.positions[ring.positions.length - 1] + 1;
  assert.equal(ring.lookup(h), ring.owners[0]);
});

test("removing one node remaps roughly 1/N of keys", () => {
  const before = new ConsistentHashRing(nodes(10), 160, md5_32);
  const after = new ConsistentHashRing(nodes(9), 160, md5_32);
  const rand = mulberry32(7);
  const samples = 50_000;
  let moved = 0;
  for (let i = 0; i < samples; i++) {
    const h = md5_32(encodeKey(`k${Math.floor(rand() * 1e9)}`));
    const a = before.lookup(h);
    const b = after.lookup(h);
    // node ids shifted: node-9 removed, node-k keeps index k for k < 9
    if (a !== b) moved++;
  }
  const ratio = moved / samples;
  assert.ok(ratio > 0.05 && ratio < 0.2, `moved ratio ${ratio}`);
});

test("md5 ring has no point collisions at this scale", () => {
  const ring = new ConsistentHashRing(nodes(50), 160, md5_32);
  assert.equal(ring.collisions, 0);
});
