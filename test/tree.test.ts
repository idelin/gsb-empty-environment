import assert from "node:assert/strict";
import { test } from "node:test";

import {
  ALLOW_BASE,
  ALLOW_ALL,
  ALLOW_NONE,
  allowAllPlus,
  allowOnly,
  buildTree,
  effectiveAllowlist,
  findNode,
  intersect,
  isAllowed,
  matches,
  migrateSubtree,
  navigate,
  opaqueOrigin,
  origin,
  walk,
  type Allowlist,
  type Origin,
} from "../index.ts";

const FEATURE = "camera";
const T = origin("https://top.test");
const A = origin("https://a.test");
const B = origin("https://b.test");
const C = origin("https://c.test");
const O = opaqueOrigin("opaque-token-1");

const ORIGINS: Origin[] = [T, A, B, C, O];

/** 小而全的允许列表宇宙：通配、全拒、点名具名来源、点名不透明来源、通配+不透明。 */
function makeLists(): Allowlist[] {
  return [
    ALLOW_ALL,
    ALLOW_NONE,
    allowOnly(T),
    allowOnly(A),
    allowOnly(B),
    allowOnly(O),
    allowAllPlus(O),
  ];
}

interface LayerPicks {
  topDeclared: Allowlist;
  aContainer: Allowlist;
  aDeclared: Allowlist;
  bContainer: Allowlist;
  bDeclared: Allowlist;
}

function buildThreeLayerTree(picks: LayerPicks) {
  return buildTree({
    id: "top",
    origin: T,
    declared: { [FEATURE]: picks.topDeclared },
    children: [
      {
        id: "frameA",
        origin: A,
        container: { [FEATURE]: picks.aContainer },
        declared: { [FEATURE]: picks.aDeclared },
        children: [
          {
            id: "frameB",
            origin: B,
            container: { [FEATURE]: picks.bContainer },
            declared: { [FEATURE]: picks.bDeclared },
            children: [],
          },
        ],
      },
    ],
  });
}

function allPicks(): LayerPicks[] {
  const result: LayerPicks[] = [];
  const lists = makeLists();
  for (const topDeclared of lists) {
    for (const aContainer of lists) {
      for (const aDeclared of lists) {
        for (const bContainer of lists) {
          for (const bDeclared of lists) {
            result.push({ topDeclared, aContainer, aDeclared, bContainer, bDeclared });
          }
        }
      }
    }
  }
  return result;
}

/**
 * 参考实现：沿根到叶路径从“不收紧”基线开始左折叠。
 * 与 effectiveAllowlist 采用同一结合方向，避免通配 + 不透明令牌在任意加括号下
 * 的非结合性掩盖真实语义。
 */
function foldPath(layers: Allowlist[]): Allowlist {
  return layers.reduce((acc, layer) => intersect(acc, layer), ALLOW_BASE);
}

test("全枚举：有效允许列表等于路径各层允许列表的逐层交集", () => {
  for (const picks of allPicks()) {
    const root = buildThreeLayerTree(picks);
    const frameA = findNode(root, "frameA")!;
    const frameB = findNode(root, "frameB")!;

    const expectedA = foldPath([
      picks.topDeclared,
      picks.aContainer,
      picks.aDeclared,
    ]);
    const expectedB = foldPath([
      picks.topDeclared,
      picks.aContainer,
      picks.aDeclared,
      picks.bContainer,
      picks.bDeclared,
    ]);

    assert.deepEqual(effectiveAllowlist(root, frameA, FEATURE), expectedA);
    assert.deepEqual(effectiveAllowlist(root, frameB, FEATURE), expectedB);
  }
});

test("全枚举：祖先逐层收紧，后代有效集合是祖先的子集", () => {
  for (const picks of allPicks()) {
    const root = buildThreeLayerTree(picks);
    const top = findNode(root, "top")!;
    const frameA = findNode(root, "frameA")!;
    const frameB = findNode(root, "frameB")!;

    const topList = effectiveAllowlist(root, top, FEATURE);
    const listA = effectiveAllowlist(root, frameA, FEATURE);
    const listB = effectiveAllowlist(root, frameB, FEATURE);

    for (const requester of ORIGINS) {
      if (matches(listA, requester)) {
        assert.equal(
          matches(topList, requester),
          true,
          "frameA 放行的来源必须已被顶层放行",
        );
      }
      if (matches(listB, requester)) {
        assert.equal(matches(listA, requester), true, "frameB 放行的来源必须已被 frameA 放行");
      }
    }
  }
});

test("全枚举：不透明来源不会被通配放行，仅在令牌显式出现时放行", () => {
  for (const picks of allPicks()) {
    const root = buildThreeLayerTree(picks);
    walk(root, (node) => {
      const effective = effectiveAllowlist(root, node, FEATURE);
      const opaqueAllowed = matches(effective, O);
      const pathLists =
        node.id === "top"
          ? [picks.topDeclared]
          : node.id === "frameA"
            ? [picks.topDeclared, picks.aContainer, picks.aDeclared]
            : [
                picks.topDeclared,
                picks.aContainer,
                picks.aDeclared,
                picks.bContainer,
                picks.bDeclared,
              ];
      const everyLayerCarriesToken = pathLists.every((list) =>
        list.explicit.has(`opaque:${O.token}`),
      );

      assert.equal(opaqueAllowed, everyLayerCarriesToken);
    });
  }
});

test("通配只作用于当前层，无法解除上层的点名限制", () => {
  const root = buildTree({
    id: "top",
    origin: T,
    declared: { [FEATURE]: allowOnly(A) },
    children: [
      {
        id: "frameA",
        origin: A,
        container: { [FEATURE]: ALLOW_ALL },
        declared: { [FEATURE]: ALLOW_ALL },
      },
    ],
  });
  const frameA = findNode(root, "frameA")!;

  assert.equal(isAllowed(root, frameA, FEATURE, A), true);
  assert.equal(isAllowed(root, frameA, FEATURE, B), false);
  assert.equal(isAllowed(root, frameA, FEATURE, C), false);
});

test("未声明的功能默认不收紧，仍可被任一层显式收紧", () => {
  const open = buildTree({
    id: "top",
    origin: T,
    children: [{ id: "frameA", origin: A, children: [] }],
  });
  const openFrame = findNode(open, "frameA")!;
  assert.equal(isAllowed(open, openFrame, "microphone", B), true);

  const locked = buildTree({
    id: "top",
    origin: T,
    declared: { microphone: ALLOW_NONE },
    children: [{ id: "frameA", origin: A, children: [] }],
  });
  const lockedFrame = findNode(locked, "frameA")!;
  assert.equal(isAllowed(locked, lockedFrame, "microphone", B), false);
});

test("子框架迁移后按新祖先路径重新求交，可能被收紧，且旧树不变", () => {
  const root = buildTree({
    id: "top",
    origin: T,
    declared: { [FEATURE]: allowOnly(A, B) },
    children: [
      {
        id: "lenient",
        origin: A,
        container: { [FEATURE]: ALLOW_ALL },
        declared: { [FEATURE]: ALLOW_ALL },
        children: [
          {
            id: "movable",
            origin: B,
            container: { [FEATURE]: ALLOW_ALL },
            declared: { [FEATURE]: ALLOW_ALL },
          },
        ],
      },
      {
        id: "strict",
        origin: C,
        container: { [FEATURE]: ALLOW_NONE },
        declared: { [FEATURE]: ALLOW_NONE },
      },
    ],
  });

  const before = findNode(root, "movable")!;
  assert.equal(isAllowed(root, before, FEATURE, B), true);

  const movedRoot = migrateSubtree(root, "movable", "strict");
  const moved = findNode(movedRoot, "movable")!;
  assert.equal(moved.origin, B);
  assert.equal(isAllowed(movedRoot, moved, FEATURE, B), false);

  const oldAgain = findNode(root, "movable")!;
  assert.equal(isAllowed(root, oldAgain, FEATURE, B), true);
  assert.equal(findNode(movedRoot, "lenient")!.children.length, 0);
});

test("迁移可替换容器策略，且禁止迁入自身后代", () => {
  const root = buildTree({
    id: "top",
    origin: T,
    declared: { [FEATURE]: ALLOW_ALL },
    children: [
      {
        id: "parentA",
        origin: A,
        children: [
          { id: "child", origin: B, container: { [FEATURE]: ALLOW_NONE }, children: [] },
        ],
      },
      { id: "parentB", origin: C, children: [] },
    ],
  });

  const reopened = migrateSubtree(root, "child", "parentB", {
    container: { [FEATURE]: ALLOW_ALL },
  });
  const child = findNode(reopened, "child")!;
  assert.equal(isAllowed(reopened, child, FEATURE, B), true);

  assert.throws(() => migrateSubtree(root, "parentA", "child"));
  assert.throws(() => migrateSubtree(root, "top", "parentB"));
});

test("导航重建子树：旧后代消失、来源与声明被替换、旧能力失效", () => {
  const root = buildTree({
    id: "top",
    origin: T,
    declared: { [FEATURE]: ALLOW_ALL },
    children: [
      {
        id: "frameA",
        origin: A,
        container: { [FEATURE]: ALLOW_ALL },
        declared: { [FEATURE]: ALLOW_ALL },
        children: [
          { id: "frameB", origin: B, declared: { [FEATURE]: ALLOW_ALL }, children: [] },
        ],
      },
    ],
  });

  const frameABefore = findNode(root, "frameA")!;
  const snapshotBefore = effectiveAllowlist(root, frameABefore, FEATURE);
  assert.equal(matches(snapshotBefore, A), true);

  const navigated = navigate(root, "frameA", C, { declared: { [FEATURE]: ALLOW_NONE } });

  assert.equal(findNode(navigated, "frameB"), undefined);
  const frameAAfter = findNode(navigated, "frameA")!;
  assert.equal(frameAAfter.origin, C);
  assert.equal(isAllowed(navigated, frameAAfter, FEATURE, A), false);
  assert.equal(isAllowed(navigated, frameAAfter, FEATURE, C), false);

  assert.equal(findNode(root, "frameB") !== undefined, true);
  assert.equal(matches(effectiveAllowlist(root, frameABefore, FEATURE), A), true);
});

test("导航到不透明来源后，只有显式携带该令牌的路径才放行", () => {
  const root = buildTree({
    id: "top",
    origin: T,
    declared: { [FEATURE]: allowAllPlus(O) },
    children: [
      {
        id: "frameA",
        origin: A,
        container: { [FEATURE]: allowAllPlus(O) },
        declared: { [FEATURE]: ALLOW_ALL },
      },
    ],
  });

  const navigated = navigate(root, "frameA", O, {
    declared: { [FEATURE]: allowOnly(O) },
  });
  const frameA = findNode(navigated, "frameA")!;

  assert.equal(isAllowed(navigated, frameA, FEATURE, O), true);
  assert.equal(isAllowed(navigated, frameA, FEATURE, opaqueOrigin("other-token")), false);
  assert.equal(isAllowed(navigated, frameA, FEATURE, T), false);
});
