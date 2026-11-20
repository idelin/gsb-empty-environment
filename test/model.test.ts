import assert from "node:assert/strict";
import { test } from "node:test";
import {
  compilePolicy,
  evalPolicy,
  matchCondition,
  validatePolicy,
} from "../src/index.ts";
import type { Packet } from "../src/index.ts";
import { ANY, allow, deny, ip, labelIs, policy, port, rule } from "../testkit/helpers.ts";

const pkt = (over: Partial<Packet> = {}): Packet => ({
  src: ip("10.0.0.1"),
  dst: ip("10.0.0.2"),
  srcPort: 12345,
  dstPort: 80,
  direction: "forward",
  ...over,
});

test("标签三位掩码条件按位匹配", () => {
  const cond = labelIs(6, 4); // 110 & label === 100 → 4, 5
  const matched = [0, 1, 2, 3, 4, 5, 6, 7].filter((l) =>
    matchCondition(cond, pkt(), l),
  );
  assert.deepEqual(matched, [4, 5]);
});

test("最高优先级先匹配，同优先级按声明顺序", () => {
  const p = compilePolicy(
    policy(
      rule(10, ANY, deny()), // 0：同优先级先声明，先匹配
      rule(10, ANY, allow()), // 1：永远不可达
      rule(20, port("dst", 80, 80), allow()), // 2：优先级最高
    ),
  );
  assert.deepEqual(evalPolicy(p, pkt({ dstPort: 80 }), 0), {
    rule: 2,
    allow: true,
    label: 0,
  });
  assert.deepEqual(evalPolicy(p, pkt({ dstPort: 81 }), 0), {
    rule: 0,
    allow: false,
    label: 0,
  });
});

test("无规则匹配时默认拒绝且标签不变", () => {
  const p = compilePolicy(policy(rule(10, port("dst", 80, 80), allow())));
  assert.deepEqual(evalPolicy(p, pkt({ dstPort: 81 }), 3), {
    rule: -1,
    allow: false,
    label: 3,
  });
});

test("允许动作可设置标签，拒绝不改变标签", () => {
  const p = compilePolicy(
    policy(
      rule(20, port("dst", 80, 80), allow(5)),
      rule(10, ANY, deny()),
    ),
  );
  assert.deepEqual(evalPolicy(p, pkt({ dstPort: 80 }), 0), {
    rule: 0,
    allow: true,
    label: 5,
  });
  assert.deepEqual(evalPolicy(p, pkt({ dstPort: 81 }), 2), {
    rule: 1,
    allow: false,
    label: 2,
  });
});

test("策略校验：规则数上限 40", () => {
  const rules = Array.from({ length: 41 }, () => rule(1, ANY, allow()));
  assert.throws(() => validatePolicy(policy(...rules)), /超过上限/);
  const ok = Array.from({ length: 40 }, () => rule(1, ANY, allow()));
  assert.doesNotThrow(() => validatePolicy(policy(...ok)));
});

test("策略校验：拒绝动作不得设置标签", () => {
  assert.throws(
    () => validatePolicy(policy(rule(1, ANY, { allow: false, setLabel: 1 }))),
    /拒绝动作不得设置标签/,
  );
});

test("策略校验：标签、端口、前缀长度范围", () => {
  assert.throws(() => validatePolicy(policy(rule(1, ANY, allow(8)))), /0\.\.7/);
  assert.throws(
    () => validatePolicy(policy(rule(1, port("dst", 100, 80), allow()))),
    /下界大于上界/,
  );
  assert.throws(
    () =>
      validatePolicy(
        policy(
          rule(1, { kind: "prefix", side: "src", prefix: 0, length: 33 }, allow()),
        ),
      ),
    /0\.\.32/,
  );
  assert.throws(
    () => validatePolicy(policy(rule(1, labelIs(7, 8), allow()))),
    /0\.\.7/,
  );
});
