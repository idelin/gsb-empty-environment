// 示例程序集,覆盖:溢出、有界循环、状态合并、不可达分支、一元取负溢出。

import {
  C, inp, V, add, mul, neg, gt, lt, ge, ne, le, and,
  type Program,
} from "./lang.ts";

// 1. 加法溢出:两个正数相加回绕为负,触发断言失败。
//    s = in0 + in1; if (in0 > 0 && in1 > 0) assert(s > 0)
//    预期最小失败输入 [1, 127](1+127=128 回绕为 -128)。
const overflowAdd: Program = {
  name: "overflow-add",
  inputs: 2,
  targetAssert: 0,
  body: [
    { kind: "assign", name: "s", expr: add(inp(0), inp(1)) },
    {
      kind: "if", id: 0,
      cond: and(gt(inp(0), C(0)), gt(inp(1), C(0))),
      then: [{ kind: "assert", id: 0, cond: gt(V("s"), C(0)) }],
      els: [],
    },
  ],
};

// 2. 有界循环:累加 in0 次 in1 后断言 s < 50;if#1 的 then 分支不可达(i <= 8)。
//    预期最小失败输入 [1, 50];in0 > 8 时循环超界被截断。
const boundedLoop: Program = {
  name: "bounded-loop",
  inputs: 2,
  targetAssert: 1,
  body: [
    { kind: "assign", name: "i", expr: C(0) },
    { kind: "assign", name: "s", expr: C(0) },
    {
      kind: "while", id: 0, bound: 8,
      cond: lt(V("i"), inp(0)),
      body: [
        { kind: "assign", name: "s", expr: add(V("s"), inp(1)) },
        { kind: "assign", name: "i", expr: add(V("i"), C(1)) },
      ],
    },
    {
      kind: "if", id: 1,
      cond: gt(V("i"), C(10)),
      then: [{ kind: "assert", id: 0, cond: C(0) }],
      els: [],
    },
    { kind: "assert", id: 1, cond: lt(V("s"), C(50)) },
  ],
};

// 3. 状态合并:if#0 两个分支都把 x 置为 1,汇合后存储相同,合并为一个状态,
//    路径条件保留为 (in0>0) ∨ (in0<=0)。断言 x != 2 在 in1 >= 0 时失败。
//    预期最小失败输入 [-128, 0]。
const mergeDemo: Program = {
  name: "merge-demo",
  inputs: 2,
  targetAssert: 0,
  body: [
    { kind: "assign", name: "x", expr: C(0) },
    {
      kind: "if", id: 0,
      cond: gt(inp(0), C(0)),
      then: [{ kind: "assign", name: "x", expr: C(1) }],
      els: [{ kind: "assign", name: "x", expr: C(1) }],
    },
    {
      kind: "if", id: 1,
      cond: ge(inp(1), C(0)),
      then: [{ kind: "assign", name: "x", expr: add(V("x"), C(1)) }],
      els: [{ kind: "assign", name: "x", expr: C(0) }],
    },
    { kind: "assert", id: 0, cond: ne(V("x"), C(2)) },
  ],
};

// 4. 不可达分支:in0 > 100 与 in0 < 0 不可能同时成立,if#1 的 then 不可达;
//    assert#1 恒真,其 fail 边不可达。程序没有任何可触发的断言失败。
const unreachable: Program = {
  name: "unreachable",
  inputs: 1,
  targetAssert: 0,
  body: [
    {
      kind: "if", id: 0,
      cond: gt(inp(0), C(100)),
      then: [
        {
          kind: "if", id: 1,
          cond: lt(inp(0), C(0)),
          then: [{ kind: "assert", id: 0, cond: C(0) }],
          els: [],
        },
        { kind: "assert", id: 1, cond: le(inp(0), C(127)) },
      ],
      els: [],
    },
  ],
};

// 5. 一元取负溢出:-(-128) 回绕为 -128,断言 x != -128 仅在 in0 = -128 时失败。
//    预期最小失败输入 [-128]。
const negOverflow: Program = {
  name: "neg-overflow",
  inputs: 1,
  targetAssert: 0,
  body: [
    { kind: "assign", name: "x", expr: neg(inp(0)) },
    { kind: "assert", id: 0, cond: ne(V("x"), C(-128)) },
  ],
};

// 6. 乘法溢出 + 循环内断言:k 累乘 in1,循环内断言 k 不溢出回绕后仍为正。
//    if (in0 >= 2) { k = in1 * in1; assert(k > 0) } —— in1=16 时 256 回绕为 0,失败。
const mulOverflow: Program = {
  name: "mul-overflow",
  inputs: 2,
  targetAssert: 0,
  body: [
    {
      kind: "if", id: 0,
      cond: ge(inp(0), C(2)),
      then: [
        { kind: "assign", name: "k", expr: mul(inp(1), inp(1)) },
        { kind: "assert", id: 0, cond: gt(V("k"), C(0)) },
      ],
      els: [],
    },
  ],
};

export const programs: Program[] = [
  overflowAdd,
  boundedLoop,
  mergeDemo,
  unreachable,
  negOverflow,
  mulOverflow,
];
