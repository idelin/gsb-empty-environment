import { describe, expect, test } from "bun:test";
import { ListModel, type Id, type Op } from "../src/list-model";

function* permutations<T>(items: readonly T[]): Generator<T[]> {
  const arr = [...items];
  if (arr.length <= 1) {
    yield arr;
    return;
  }
  for (let i = 0; i < arr.length; i++) {
    const [head] = arr.splice(i, 1);
    for (const rest of permutations(arr)) yield [head, ...rest];
    arr.splice(i, 0, head);
  }
}

function runAll(ops: readonly Op[]): ListModel {
  const model = new ListModel();
  model.applyAll(ops);
  return model;
}

function visibleIds(model: ListModel): Id[] {
  return model.toArray().map((n) => n.id);
}

/** 对同一封闭操作集的所有排列，断言最终顺序一致且诊断为空 */
function expectConverges(ops: Op[], expected: Id[]): void {
  const expectedDiag = { pendingInserts: [], missingDependencies: [] };
  let count = 0;
  for (const perm of permutations(ops)) {
    const model = runAll(perm);
    expect(visibleIds(model)).toEqual(expected);
    expect(model.diagnostics()).toEqual(expectedDiag);
    count++;
  }
  expect(count).toBe(factorial(ops.length));
}

function factorial(n: number): number {
  return n <= 1 ? 1 : n * factorial(n - 1);
}

/** 确定性伪随机数（mulberry32），用于可复现的随机场景 */
function rng(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

describe("乱序收敛", () => {
  const script: Op[] = [
    { kind: "insert", id: "a", pred: null, value: "A" },
    { kind: "insert", id: "b", pred: "a", value: "B" },
    { kind: "insert", id: "c", pred: "a", value: "C" },
    { kind: "insert", id: "d", pred: "b", value: "D" },
    { kind: "delete", id: "b" },
    { kind: "insert", id: "e", pred: "d", value: "E" },
  ];
  // b 被删除但仍是 d 的锚点：a → (b✝ → d → e), c
  const expected: Id[] = ["a", "d", "e", "c"];

  test("全部 720 种排列收敛到同一顺序", () => {
    expectConverges(script, expected);
  });

  test("乱序 + 重复操作仍收敛", () => {
    let count = 0;
    for (const perm of permutations(script)) {
      // 每种排列混入重复操作：首尾各重放两条
      const noisy = [perm[2], ...perm, perm[0], perm[5], perm[2]];
      const model = runAll(noisy);
      expect(visibleIds(model)).toEqual(expected);
      expect(model.diagnostics().missingDependencies).toEqual([]);
      count++;
    }
    expect(count).toBe(720);
  });

  test("同前驱兄弟按 id 字典序", () => {
    const ops: Op[] = [
      { kind: "insert", id: "m", pred: null, value: "M" },
      { kind: "insert", id: "z", pred: "m", value: "Z" },
      { kind: "insert", id: "f", pred: "m", value: "F" },
      { kind: "insert", id: "q", pred: "m", value: "Q" },
    ];
    expectConverges(ops, ["m", "f", "q", "z"]);
  });

  test("随机封闭操作集：所有排列与因果序结果一致", () => {
    for (let seed = 1; seed <= 10; seed++) {
      const rand = rng(seed);
      const ops: Op[] = [];
      const inserted: Id[] = [];
      for (let i = 0; i < 4; i++) {
        const id = `n${seed}_${i}`;
        const pred =
          inserted.length === 0 || rand() < 0.3
            ? null
            : inserted[Math.floor(rand() * inserted.length)];
        ops.push({ kind: "insert", id, pred, value: id.toUpperCase() });
        inserted.push(id);
      }
      for (let i = 0; i < 2; i++) {
        const victim = inserted[Math.floor(rand() * inserted.length)];
        ops.push({ kind: "delete", id: victim });
      }
      const reference = visibleIds(runAll(ops));
      expectConverges(ops, reference);
    }
  });
});

describe("缺依赖诊断", () => {
  test("未知前驱的 insert 被暂存并上报，前驱到达后自动挂载", () => {
    const model = new ListModel();
    model.apply({ kind: "insert", id: "x", pred: "ghost", value: "X" });

    expect(model.toArray()).toEqual([]);
    expect(model.diagnostics()).toEqual({
      pendingInserts: [{ kind: "insert", id: "x", pred: "ghost", value: "X" }],
      missingDependencies: ["ghost"],
    });

    model.apply({ kind: "insert", id: "ghost", pred: null, value: "G" });
    expect(visibleIds(model)).toEqual(["ghost", "x"]);
    expect(model.diagnostics()).toEqual({
      pendingInserts: [],
      missingDependencies: [],
    });
  });

  test("暂存可级联：链式依赖按相反顺序到达", () => {
    const model = new ListModel();
    model.apply({ kind: "insert", id: "c", pred: "b", value: "C" });
    model.apply({ kind: "insert", id: "b", pred: "a", value: "B" });
    // b 的 insert 仍在暂存区，a、b 均属未到达的依赖
    expect(model.diagnostics().missingDependencies).toEqual(["a", "b"]);
    model.apply({ kind: "insert", id: "a", pred: null, value: "A" });
    expect(visibleIds(model)).toEqual(["a", "b", "c"]);
    expect(model.diagnostics().missingDependencies).toEqual([]);
  });

  test("delete 先至：占位墓碑上报缺依赖，insert 到达后保持删除态", () => {
    const model = new ListModel();
    model.apply({ kind: "delete", id: "gone" });
    expect(model.diagnostics()).toEqual({
      pendingInserts: [],
      missingDependencies: ["gone"],
    });

    model.apply({ kind: "insert", id: "gone", pred: null, value: "G" });
    expect(model.toArray()).toEqual([]); // 墓碑不复活
    expect(model.diagnostics().missingDependencies).toEqual([]);
  });

  test("占位墓碑立即锚定后继：del b → ins d(pred b) → ins b", () => {
    const model = new ListModel();
    model.apply({ kind: "insert", id: "a", pred: null, value: "A" });
    model.apply({ kind: "delete", id: "b" });
    model.apply({ kind: "insert", id: "d", pred: "b", value: "D" });
    // b 的 insert 未到时，d 随占位墓碑浮动，不可见
    expect(model.toArray()).toEqual([{ id: "a", value: "A" }]);

    model.apply({ kind: "insert", id: "b", pred: "a", value: "B" });
    expect(visibleIds(model)).toEqual(["a", "d"]); // b 为墓碑，d 锚定其下
    expect(model.diagnostics().missingDependencies).toEqual([]);
  });
});
