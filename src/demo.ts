/**
 * Demo: exhaustiveness / redundancy checking with minimal-depth witnesses,
 * cross-validated by depth-bounded enumeration.
 */

import { analyze, showVector } from "./check";
import { ctor, varp, _ } from "./pattern";
import { TypeEnv } from "./types";

const env: TypeEnv = {
  Bool: {
    name: "Bool",
    ctors: [
      { name: "True", fields: [] },
      { name: "False", fields: [] },
    ],
  },
  Nat: {
    name: "Nat",
    ctors: [
      { name: "Zero", fields: [] },
      { name: "Succ", fields: ["Nat"] },
    ],
  },
  Option: {
    name: "Option",
    ctors: [
      { name: "None", fields: [] },
      { name: "Some", fields: ["Nat"] },
    ],
  },
  List: {
    name: "List",
    ctors: [
      { name: "Nil", fields: [] },
      { name: "Cons", fields: ["Nat", "List"] },
    ],
  },
  Tree: {
    name: "Tree",
    ctors: [
      { name: "Leaf", fields: [] },
      { name: "Node", fields: ["Tree", "Nat", "Tree"] },
    ],
  },
};

function showReport(title: string, r: ReturnType<typeof analyze>): void {
  console.log(`\n=== ${title} ===`);
  console.log(`穷尽性: ${r.exhaustive ? "是" : "否"}`);
  if (r.counterexample) {
    console.log(
      `反例见证 (最小构造深度 ${r.counterexampleDepth}): ${showVector(r.counterexample)}`
    );
  }
  for (const b of r.branches) {
    if (b.status === "unreachable") {
      console.log(`  分支 ${b.index}: 不可达 (被前面分支完全覆盖)`);
    } else {
      console.log(
        `  分支 ${b.index}: 可达, 见证 (深度 ${b.witnessDepth}): ${showVector(b.witness!)}`
      );
    }
  }
  const cv = r.crossValidation!;
  console.log(
    `交叉验证: 枚举深度 ≤ ${cv.depthBound}, 枚举值 ${cv.enumeratedValues} 个` +
      `${cv.truncated ? " (截断)" : ""}, 结果${cv.consistent ? "一致 ✓" : "不一致 ✗"}`
  );
  if (cv.unreachableUpToDepth.length > 0) {
    console.log(`  枚举下亦不可达的分支: [${cv.unreachableUpToDepth.join(", ")}]`);
  }
  for (const f of cv.findings) console.log(`  - ${f}`);
}

// 1. Option: 非穷尽, 最小反例 None
showReport(
  "Option: [Some(_)]",
  analyze(env, ["Option"], [[ctor("Some", varp("x"))]])
);

// 2. Option: 穷尽但含不可达分支
showReport(
  "Option: [None, Some(_), Some(Zero)]",
  analyze(env, ["Option"], [
    [ctor("None")],
    [ctor("Some", _)],
    [ctor("Some", ctor("Zero"))],
  ])
);

// 3. 递归类型 List: 非穷尽, 最小深度反例
showReport(
  "List: [Nil, Cons(_, Nil)]",
  analyze(env, ["List"], [
    [ctor("Nil")],
    [ctor("Cons", _, ctor("Nil"))],
  ])
);

// 4. 递归类型 List: 穷尽 + 不可达分支
showReport(
  "List: [Nil, Cons(_, _), Cons(Succ(_), Cons(_, Nil))]",
  analyze(env, ["List"], [
    [ctor("Nil")],
    [ctor("Cons", _, _)],
    [ctor("Cons", ctor("Succ", _), ctor("Cons", _, ctor("Nil")))],
  ])
);

// 5. 嵌套模式 Tree: 非穷尽
showReport(
  "Tree: [Leaf, Node(Leaf, _, _)]",
  analyze(env, ["Tree"], [
    [ctor("Leaf")],
    [ctor("Node", ctor("Leaf"), _, _)],
  ])
);

// 6. 嵌套模式 Tree: 穷尽 + 被覆盖的嵌套分支
showReport(
  "Tree: [Leaf, Node(Leaf,_,Leaf), Node(_,_,_), Node(Leaf,Zero,_)]",
  analyze(env, ["Tree"], [
    [ctor("Leaf")],
    [ctor("Node", ctor("Leaf"), _, ctor("Leaf"))],
    [ctor("Node", _, _, _)],
    [ctor("Node", ctor("Leaf"), ctor("Zero"), _)],
  ])
);

// 7. 多 scrutinee: Bool × Bool
showReport(
  "Bool × Bool: [(True, True), (False, False)]",
  analyze(env, ["Bool", "Bool"], [
    [ctor("True"), ctor("True")],
    [ctor("False"), ctor("False")],
  ])
);

// 8. 深层嵌套递归模式: 验证递归类型不会无限展开
showReport(
  "List: 深层嵌套 [Cons(_, Cons(_, Cons(Zero, _))), Nil, Cons(_, _)]",
  analyze(env, ["List"], [
    [ctor("Cons", _, ctor("Cons", _, ctor("Cons", ctor("Zero"), _)))],
    [ctor("Nil")],
    [ctor("Cons", _, _)],
  ])
);
