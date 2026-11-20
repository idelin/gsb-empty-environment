/**
 * 差分测试：把地址域缩小到 10.0.0.0/28（16 个地址）、端口域缩小到
 * 少数代表值，对域内全部五元组逐一做标签对可达性搜索（全枚举），
 * 并与基于抽象类别的证书结果逐类比对，确认二者完全一致。
 */

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  checkChange,
  compilePolicy,
  findViolations,
  reachablePairs,
} from "../src/index.ts";
import type {
  ClassInfo,
  CompiledPolicy,
  Condition,
  Policy,
} from "../src/index.ts";
import { ip } from "../testkit/helpers.ts";

// 确定性伪随机数（LCG），保证测试可复现。
function makeRnd(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state * 1103515245 + 12345) & 0x7fffffff;
    return state / 0x7fffffff;
  };
}

const PREFIX_POOL: [string, number][] = [
  ["10.0.0.0", 28],
  ["10.0.0.0", 29],
  ["10.0.0.8", 29],
];
const PORT_POOL: [number, number][] = [
  [0, 80],
  [81, 1023],
  [1024, 2048],
];

function randCondition(rnd: () => number, depth: number): Condition {
  const r = rnd();
  if (depth > 0 && r >= 0.6) {
    if (r < 0.73) {
      return { kind: "and", all: [randCondition(rnd, depth - 1), randCondition(rnd, depth - 1)] };
    }
    if (r < 0.86) {
      return { kind: "or", any: [randCondition(rnd, depth - 1), randCondition(rnd, depth - 1)] };
    }
    return { kind: "not", cond: randCondition(rnd, depth - 1) };
  }
  const leaf = rnd();
  if (leaf < 0.3) {
    const [base, length] = PREFIX_POOL[Math.floor(rnd() * PREFIX_POOL.length)];
    return { kind: "prefix", side: rnd() < 0.5 ? "src" : "dst", prefix: ip(base), length };
  }
  if (leaf < 0.6) {
    const [min, max] = PORT_POOL[Math.floor(rnd() * PORT_POOL.length)];
    return { kind: "port", side: rnd() < 0.5 ? "src" : "dst", min, max };
  }
  if (leaf < 0.8) {
    return { kind: "direction", direction: rnd() < 0.5 ? "forward" : "reverse" };
  }
  return {
    kind: "label",
    mask: Math.floor(rnd() * 8),
    value: Math.floor(rnd() * 8),
  };
}

function randPolicy(rnd: () => number): Policy {
  const count = 1 + Math.floor(rnd() * 3);
  const rules = [];
  for (let i = 0; i < count; i++) {
    const allow = rnd() < 0.7;
    rules.push({
      priority: Math.floor(rnd() * 4),
      when: randCondition(rnd, 2),
      action:
        allow && rnd() < 0.5
          ? { allow: true, setLabel: Math.floor(rnd() * 8) }
          : { allow },
    });
  }
  return { rules };
}

interface Tuple {
  src: number;
  dst: number;
  srcPort: number;
  dstPort: number;
}

function syntheticClass(t: Tuple): ClassInfo {
  return {
    id: 0,
    srcLo: t.src,
    srcHi: t.src,
    dstLo: t.dst,
    dstHi: t.dst,
    srcPortLo: t.srcPort,
    srcPortHi: t.srcPort,
    dstPortLo: t.dstPort,
    dstPortHi: t.dstPort,
    rep: { src: t.src, dst: t.dst, srcPort: t.srcPort, dstPort: t.dstPort },
  };
}

function pairSetOf(pairs: { oldLabel: number; newLabel: number }[]): Set<string> {
  return new Set(pairs.map((p) => `${p.oldLabel}:${p.newLabel}`));
}

function violationSetOf(
  vs: { oldLabel: number; newLabel: number; direction: string; newRule: number; oldRule: number }[],
): Set<string> {
  return new Set(
    vs.map((v) => `${v.oldLabel}:${v.newLabel}:${v.direction}:${v.newRule}:${v.oldRule}`),
  );
}

test("缩小地址域上的全枚举与证书逐类一致", () => {
  const addresses = Array.from({ length: 16 }, (_, i) => ip("10.0.0.0") + i);
  const ports = [0, 80, 81, 1023, 1024, 2048, 65535];

  for (let seed = 1; seed <= 6; seed++) {
    const rnd = makeRnd(seed * 7919);
    const oldPolicy = randPolicy(rnd);
    const newPolicy = randPolicy(rnd);
    const oldCompiled = compilePolicy(oldPolicy);
    const newCompiled = compilePolicy(newPolicy);
    const result = checkChange(oldPolicy, newPolicy);

    const classById = result.certificate.classes;
    const findClass = (t: Tuple) =>
      classById.find(
        (c) =>
          t.src >= c.src[0] && t.src <= c.src[1] &&
          t.dst >= c.dst[0] && t.dst <= c.dst[1] &&
          t.srcPort >= c.srcPort[0] && t.srcPort <= c.srcPort[1] &&
          t.dstPort >= c.dstPort[0] && t.dstPort <= c.dstPort[1],
      );

    for (const src of addresses) {
      for (const dst of addresses) {
        for (const srcPort of ports) {
          for (const dstPort of ports) {
            const tuple = { src, dst, srcPort, dstPort };
            const cls = findClass(tuple);
            assert.ok(cls, `元组应落在某个类别中: ${JSON.stringify(tuple)}`);

            // 对该具体元组独立做标签对可达性搜索与授权核对。
            const point = syntheticClass(tuple);
            const pairs = reachablePairs(oldCompiled, newCompiled, point);
            const tupleViolations = new Set<string>();
            for (const pair of pairs) {
              for (const v of findViolations(
                oldCompiled,
                newCompiled,
                point,
                pair.oldLabel,
                pair.newLabel,
              )) {
                tupleViolations.add(
                  `${v.oldLabel}:${v.newLabel}:${v.direction}:${v.newRule}:${v.oldRule}`,
                );
              }
            }

            const certPairs = result.certificate.pairs.filter((p) => p.classId === cls.id);
            const certViolations = result.violations.filter((v) => v.classId === cls.id);

            assert.deepEqual(
              pairSetOf(pairs),
              pairSetOf(certPairs),
              `seed=${seed} 元组 ${JSON.stringify(tuple)} 的可达标签对与类别 ${cls.id} 不一致`,
            );
            assert.deepEqual(
              tupleViolations,
              violationSetOf(certViolations),
              `seed=${seed} 元组 ${JSON.stringify(tuple)} 的违规集合与类别 ${cls.id} 不一致`,
            );
          }
        }
      }
    }
  }
});

test("类别区间在两个维度上完整平铺定义域", () => {
  const rnd = makeRnd(20261120);
  for (let i = 0; i < 6; i++) {
    const oldPolicy = randPolicy(rnd);
    const newPolicy = randPolicy(rnd);
    const result = checkChange(oldPolicy, newPolicy);
    const classes = result.certificate.classes;

    const addrIntervals = [...new Set(classes.map((c) => c.src.join(":")))].sort(
      (a, b) => Number(a.split(":")[0]) - Number(b.split(":")[0]),
    );
    assert.equal(addrIntervals[0].split(":")[0], "0");
    assert.equal(addrIntervals[addrIntervals.length - 1].split(":")[1], String(0xffffffff));
    for (let k = 1; k < addrIntervals.length; k++) {
      const prevHi = Number(addrIntervals[k - 1].split(":")[1]);
      const nextLo = Number(addrIntervals[k].split(":")[0]);
      assert.equal(nextLo, prevHi + 1, "地址区间必须无缝衔接");
    }

    const portIntervals = [...new Set(classes.map((c) => c.srcPort.join(":")))].sort(
      (a, b) => Number(a.split(":")[0]) - Number(b.split(":")[0]),
    );
    assert.equal(portIntervals[0].split(":")[0], "0");
    assert.equal(portIntervals[portIntervals.length - 1].split(":")[1], "65535");
    for (let k = 1; k < portIntervals.length; k++) {
      const prevHi = Number(portIntervals[k - 1].split(":")[1]);
      const nextLo = Number(portIntervals[k].split(":")[0]);
      assert.equal(nextLo, prevHi + 1, "端口区间必须无缝衔接");
    }
  }
});
