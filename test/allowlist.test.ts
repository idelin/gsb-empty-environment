import assert from "node:assert/strict";
import { test } from "node:test";

import {
  ALLOW_BASE,
  ALLOW_ALL,
  ALLOW_NONE,
  allowAllPlus,
  allowOnly,
  intersect,
  matches,
  opaqueOrigin,
  origin,
  sameOrigin,
} from "../index.ts";

test("具名来源按字符串判定同源", () => {
  assert.equal(sameOrigin(origin("https://a.test"), origin("https://a.test")), true);
  assert.equal(sameOrigin(origin("https://a.test"), origin("https://b.test")), false);
});

test("不透明来源默认每次都是新的，仅相同令牌相等", () => {
  const first = opaqueOrigin();
  const second = opaqueOrigin();
  assert.equal(sameOrigin(first, second), false);
  assert.equal(sameOrigin(first, opaqueOrigin(first.token)), true);
  assert.equal(sameOrigin(first, origin(first.token)), false);
});

test("通配匹配任意具名来源，但不匹配不透明来源", () => {
  assert.equal(matches(ALLOW_ALL, origin("https://any.test")), true);
  assert.equal(matches(ALLOW_ALL, opaqueOrigin()), false);
  assert.equal(matches(ALLOW_NONE, origin("https://any.test")), false);
});

test("不透明来源只凭自身令牌在显式集合中匹配", () => {
  const secret = opaqueOrigin("token-secret");
  const list = allowOnly(secret);
  assert.equal(matches(list, opaqueOrigin("token-secret")), true);
  assert.equal(matches(list, opaqueOrigin("token-other")), false);
  assert.equal(matches(list, origin("token-secret")), false);
});

test("通配加显式不透明令牌可同时覆盖两类来源", () => {
  const secret = opaqueOrigin("token-x");
  const list = allowAllPlus(secret);
  assert.equal(matches(list, origin("https://named.test")), true);
  assert.equal(matches(list, opaqueOrigin("token-x")), true);
  assert.equal(matches(list, opaqueOrigin("token-y")), false);
});

test("交集使通配失效并对显式集合求交", () => {
  const a = origin("https://a.test");
  const b = origin("https://b.test");
  const c = origin("https://c.test");
  const wide = allowAllPlus(a);
  const narrow = allowOnly(a, b);
  const joint = intersect(wide, narrow);

  assert.equal(joint.wildcard, false);
  assert.equal(matches(joint, a), true);
  assert.equal(matches(joint, b), true);
  assert.equal(matches(joint, c), false);

  const strictJoint = intersect(allowOnly(a, b), allowOnly(a));
  assert.equal(matches(strictJoint, a), true);
  assert.equal(matches(strictJoint, b), false);
  assert.equal(matches(strictJoint, c), false);
});

test("交集是单调收紧的：结果集合是任一输入的子集", () => {
  const a = origin("https://a.test");
  const b = origin("https://b.test");
  const joint = intersect(allowOnly(a, b), allowOnly(a));
  assert.equal(matches(joint, a), true);
  assert.equal(matches(joint, b), false);
});

test("ALLOW_BASE 是交集单位元，且不会在首层丢掉不透明令牌", () => {
  const secret = opaqueOrigin("token-base");
  const layer = allowAllPlus(secret);
  const folded = intersect(ALLOW_BASE, layer);

  assert.deepEqual(folded, layer);
  assert.equal(matches(folded, secret), true);
  assert.equal(matches(intersect(layer, ALLOW_BASE), secret), true);
});
