import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { compare, diffTrees, semanticEqual, stableStringify } from "../index.ts";
import type {
  ChildNode,
  ErrorCategory,
  ParseOutcome,
  ParserCallback,
  TypeNode,
} from "../src/types.ts";

function range(start: number, end: number): { start: number; end: number } {
  return { start, end };
}

function leaf(
  type: string,
  value: unknown,
  start: number,
  end: number,
  encoding?: string,
): TypeNode {
  const node: TypeNode = { type, value, range: range(start, end) };
  if (encoding !== undefined) node.encoding = encoding;
  return node;
}

function container(
  type: string,
  children: ChildNode[],
  start: number,
  end: number,
  ordered?: boolean,
): TypeNode {
  const node: TypeNode = { type, range: range(start, end), children };
  if (ordered !== undefined) node.ordered = ordered;
  return node;
}

function field(name: string, node: TypeNode): ChildNode {
  return { name, node };
}

function accept(tree: TypeNode, consumed: number): ParseOutcome {
  return { accepted: true, tree, consumed };
}

function reject(
  category: ErrorCategory,
  start?: number,
  end?: number,
  consumed?: number,
  tree?: TypeNode,
): ParseOutcome {
  const outcome: ParseOutcome = { accepted: false, error: { category } };
  if (start !== undefined && end !== undefined) outcome.error.range = range(start, end);
  if (consumed !== undefined) outcome.consumed = consumed;
  if (tree) outcome.tree = tree;
  return outcome;
}

function parser(outcome: ParseOutcome): ParserCallback {
  return () => outcome;
}

function categories(result: ReturnType<typeof compare>): string[] {
  return result.divergences.map((item) => item.category);
}

describe("重复字段（ordered 容器，按位置比较）", () => {
  it("同名字段重复且一致：无分歧", () => {
    const build = (): TypeNode =>
      container("Msg", [
        field("tag", leaf("VarInt", 1n, 0, 1)),
        field("tag", leaf("VarInt", 2n, 1, 2)),
      ], 0, 2);
    const result = compare(
      new Uint8Array(2),
      parser(accept(build(), 2)),
      parser(accept(build(), 2)),
    );
    assert.equal(result.equivalent, true);
    assert.deepEqual(categories(result), []);
  });

  it("第二个重复字段值不同：最小路径定位到 index=1 并停止", () => {
    const left = container("Msg", [
      field("tag", leaf("VarInt", 1n, 0, 1)),
      field("tag", leaf("VarInt", 2n, 1, 2)),
      field("tag", leaf("VarInt", 3n, 2, 3)),
    ], 0, 3);
    const right = container("Msg", [
      field("tag", leaf("VarInt", 1n, 0, 1)),
      field("tag", leaf("VarInt", 9n, 1, 2)),
      field("tag", leaf("VarInt", 3n, 2, 3)),
    ], 0, 3);
    const divergences = diffTrees(left, right);
    assert.equal(divergences.length, 1);
    assert.equal(divergences[0].category, "value_mismatch");
    assert.deepEqual(divergences[0].path, [
      { name: "tag", index: 1, type: "VarInt" },
    ]);
    assert.equal(divergences[0].left?.value, 2n);
    assert.equal(divergences[0].right?.value, 9n);
  });

  it("重复字段数量不同：有序容器报 cardinality_mismatch，证据绑定容器区间", () => {
    const left = container("Msg", [
      field("tag", leaf("VarInt", 1n, 0, 1)),
      field("tag", leaf("VarInt", 2n, 1, 2)),
    ], 0, 2);
    const right = container("Msg", [
      field("tag", leaf("VarInt", 1n, 0, 1)),
    ], 0, 1);
    const divergences = diffTrees(left, right);
    assert.equal(divergences.length, 1);
    assert.equal(divergences[0].category, "cardinality_mismatch");
    assert.deepEqual(divergences[0].left?.range, range(0, 2));
    assert.deepEqual(divergences[0].right?.range, range(0, 1));
  });
});

describe("无序集合：仅 ordered === false 可重排，区间随节点绑定", () => {
  const orderedNodes: ChildNode[] = [
    field("h", leaf("String", "a", 0, 2)),
    field("h", leaf("String", "b", 2, 4)),
    field("h", leaf("String", "c", 4, 6)),
  ];
  const buildSet = (ordered?: boolean): TypeNode =>
    container("HeaderSet", orderedNodes, 0, 6, ordered);

  it("显式无序集合重排：无语义分歧，且不产生 range_mismatch", () => {
    const left = buildSet(false);
    const right = container("HeaderSet", [
      field("h", leaf("String", "c", 4, 6)),
      field("h", leaf("String", "a", 0, 2)),
      field("h", leaf("String", "b", 2, 4)),
    ], 0, 6, false);
    assert.deepEqual(diffTrees(left, right), []);
  });

  it("未标无序（默认有序）的重排：order_mismatch", () => {
    const left = buildSet();
    const right = container("HeaderSet", [
      field("h", leaf("String", "b", 2, 4)),
      field("h", leaf("String", "a", 0, 2)),
      field("h", leaf("String", "c", 4, 6)),
    ], 0, 6);
    const divergences = diffTrees(left, right);
    assert.equal(divergences.length, 1);
    assert.equal(divergences[0].category, "order_mismatch");
    assert.equal(divergences[0].semantic, true);
  });

  it("仅一侧标记无序时按有序处理（保守不重排）", () => {
    const left = buildSet(false);
    const right = container("HeaderSet", [
      field("h", leaf("String", "b", 2, 4)),
      field("h", leaf("String", "a", 0, 2)),
      field("h", leaf("String", "c", 4, 6)),
    ], 0, 6, true);
    assert.equal(diffTrees(left, right)[0].category, "order_mismatch");
  });

  it("无序多重集缺少一个重复元素：仅一条 cardinality_mismatch，单侧证据", () => {
    const left = container("Set", [
      field("e", leaf("VarInt", 7n, 0, 1)),
      field("e", leaf("VarInt", 7n, 1, 2)),
    ], 0, 2, false);
    const right = container("Set", [
      field("e", leaf("VarInt", 7n, 0, 1)),
    ], 0, 1, false);
    const divergences = diffTrees(left, right);
    assert.equal(divergences.length, 1);
    assert.equal(divergences[0].category, "cardinality_mismatch");
    assert.deepEqual(divergences[0].path, [
      { name: "e", index: 1, type: "VarInt" },
    ]);
    assert.deepEqual(divergences[0].left?.range, range(1, 2));
    assert.equal(divergences[0].right, null);
  });

  it("重排后证据仍绑定各侧原始字节区间，不因位置不同报 range_mismatch", () => {
    const left = container("Set", [
      field("h", leaf("String", "a", 0, 2)),
    ], 0, 2, false);
    const right = container("Set", [
      field("h", leaf("String", "a", 8, 10)),
    ], 8, 10, false);
    assert.deepEqual(diffTrees(left, right), []);
  });

  it("重排后配对元素内部的等值异编码仍被捕获（informational）", () => {
    const left = container("Set", [
      field("h", leaf("VarInt", 1n, 0, 1, "canonical")),
      field("h", leaf("VarInt", 2n, 1, 2)),
    ], 0, 2, false);
    const right = container("Set", [
      field("h", leaf("VarInt", 2n, 4, 5)),
      field("h", leaf("VarInt", 1n, 6, 7, "non-canonical")),
    ], 4, 7, false);
    const divergences = diffTrees(left, right);
    assert.deepEqual(
      divergences.map((item) => item.category),
      ["encoding_mismatch"],
    );
    assert.equal(divergences[0].semantic, false);
    assert.equal(divergences[0].left?.encoding, "canonical");
    assert.equal(divergences[0].right?.encoding, "non-canonical");
  });
});

describe("截断输入", () => {
  it("一边截断拒绝、一边接受：acceptance_mismatch，禁止归并", () => {
    const fullTree = container("Msg", [
      field("a", leaf("VarInt", 1n, 0, 1)),
    ], 0, 1);
    const result = compare(
      new Uint8Array(1),
      parser(accept(fullTree, 1)),
      parser(reject("truncated", 1, 1, 1)),
    );
    assert.equal(result.equivalent, false);
    assert.equal(categories(result)[0], "acceptance_mismatch");
    assert.equal(result.left.accepted, true);
    assert.equal(result.right.accepted, false);
    assert.equal(result.divergences[0].right?.error?.category, "truncated");
    assert.equal(result.divergences[0].left, null);
  });

  it("截断侧保留部分树：裁决不归并，部分树仍可在结果中检查", () => {
    const fullTree = container("Msg", [
      field("a", leaf("VarInt", 1n, 0, 1)),
      field("b", leaf("VarInt", 2n, 1, 2)),
    ], 0, 2);
    const partialTree = container("Msg", [
      field("a", leaf("VarInt", 1n, 0, 1)),
    ], 0, 1);
    const result = compare(
      new Uint8Array(2),
      parser(accept(fullTree, 2)),
      parser(reject("truncated", 1, 2, 1, partialTree)),
    );
    assert.equal(categories(result)[0], "acceptance_mismatch");
    assert.equal(result.trees.right?.children?.length, 1);
    assert.equal(result.trees.left?.children?.length, 2);
  });
});

describe("接受/拒绝裁决不可归并", () => {
  it("双方都拒绝且类别相同、位置不同：只报 informational 位置差异", () => {
    const result = compare(
      new Uint8Array(4),
      parser(reject("malformed", 2, 3)),
      parser(reject("malformed", 0, 1)),
    );
    assert.equal(result.equivalent, true);
    assert.deepEqual(categories(result), ["error_location_mismatch"]);
    assert.equal(result.divergences[0].semantic, false);
  });

  it("双方都拒绝但类别不同：reject_category_mismatch 属语义分歧", () => {
    const result = compare(
      new Uint8Array(4),
      parser(reject("truncated", 3, 4)),
      parser(reject("invalid_value", 3, 4)),
    );
    assert.equal(result.equivalent, false);
    assert.deepEqual(categories(result), ["reject_category_mismatch"]);
    assert.equal(result.divergences[0].left?.error?.category, "truncated");
    assert.equal(result.divergences[0].right?.error?.category, "invalid_value");
  });

  it("解析回调抛异常被归一化为 internal 拒绝，不与接受侧归并", () => {
    const throwing: ParserCallback = () => {
      throw new Error("boom");
    };
    const result = compare(
      new Uint8Array(1),
      parser(accept(leaf("Root", 0n, 0, 1), 1)),
      throwing,
    );
    assert.equal(result.equivalent, false);
    assert.equal(categories(result)[0], "acceptance_mismatch");
    assert.equal(result.right.error?.thrown, true);
    assert.equal(result.right.error?.category, "internal");
  });
});

describe("消费字节数与等值不同编码", () => {
  it("消费字节数不同：consumed_length_mismatch 计入语义分歧", () => {
    const tree = leaf("VarInt", 1n, 0, 1);
    const result = compare(
      new Uint8Array(3),
      parser(accept(tree, 1)),
      parser(accept(tree, 3)),
    );
    assert.equal(categories(result)[0], "consumed_length_mismatch");
    assert.equal(result.divergences[0].left?.consumed, 1);
    assert.equal(result.divergences[0].right?.consumed, 3);
    assert.equal(result.equivalent, false);
  });

  it("等值不同编码：encoding_mismatch 为 informational，仍判等价", () => {
    const left = leaf("VarInt", 300n, 0, 2, "canonical");
    const right = leaf("VarInt", 300n, 0, 2, "non-canonical");
    const divergences = diffTrees(left, right);
    assert.deepEqual(
      divergences.map((item) => item.category),
      ["encoding_mismatch"],
    );
    assert.equal(divergences[0].semantic, false);
    assert.equal(divergences[0].left?.encoding, "canonical");
    assert.equal(divergences[0].right?.encoding, "non-canonical");
  });

  it("等值但字节区间不同：range_mismatch 保留双方原始区间", () => {
    const divergences = diffTrees(
      leaf("VarInt", 1n, 0, 1),
      leaf("VarInt", 1n, 5, 6),
    );
    assert.equal(divergences[0].category, "range_mismatch");
    assert.deepEqual(divergences[0].left?.range, range(0, 1));
    assert.deepEqual(divergences[0].right?.range, range(5, 6));
    assert.equal(divergences[0].semantic, false);
  });
});

describe("真实解析回调端到端（varint 严格/宽松实现）", () => {
  interface VarintResult {
    value: bigint;
    size: number;
    encoding: "canonical" | "non-canonical";
  }

  function decodeVarint(input: Uint8Array, strict: boolean): VarintResult {
    let value = 0n;
    let shift = 0n;
    let index = 0;
    for (; index < input.length; index += 1) {
      const byte = input[index];
      value |= BigInt(byte & 0x7f) << shift;
      shift += 7n;
      if ((byte & 0x80) === 0) {
        let encoding: VarintResult["encoding"] = "canonical";
        for (let i = 0; i < index; i += 1) {
          if ((input[i] & 0x7f) === 0) {
            encoding = "non-canonical";
            break;
          }
        }
        if (strict && encoding === "non-canonical") {
          throw new Error("non-canonical varint");
        }
        return { value, size: index + 1, encoding };
      }
    }
    throw new Error("truncated varint");
  }

  const strictParser: ParserCallback = (input) => {
    try {
      const decoded = decodeVarint(input, true);
      return accept(
        leaf("VarInt", decoded.value, 0, decoded.size, decoded.encoding),
        decoded.size,
      );
    } catch {
      return {
        accepted: false,
        error: { category: "malformed", range: range(0, input.length) },
      };
    }
  };

  const lenientParser: ParserCallback = (input) => {
    try {
      const decoded = decodeVarint(input, false);
      return accept(
        leaf("VarInt", decoded.value, 0, decoded.size, decoded.encoding),
        decoded.size,
      );
    } catch {
      return {
        accepted: false,
        error: { category: "truncated", range: range(0, input.length) },
      };
    }
  };

  it("非规范 varint：严格侧拒绝、宽松侧接受 => acceptance_mismatch", () => {
    // 300 的非规范编码：80 82 02（首个 7 位组为冗余 0，规范形式为 ac 02）
    const result = compare(
      new Uint8Array([0x80, 0x82, 0x02]),
      strictParser,
      lenientParser,
    );
    assert.equal(categories(result)[0], "acceptance_mismatch");
    assert.equal(result.left.accepted, false);
    assert.equal(result.right.accepted, true);
    assert.equal(result.right.consumed, 3);
  });

  it("规范 varint：双方接受且消费一致，无分歧", () => {
    const result = compare(new Uint8Array([0xac, 0x02]), strictParser, lenientParser);
    assert.equal(result.equivalent, true);
    assert.equal(result.left.consumed, 2);
    assert.equal(result.right.consumed, 2);
    assert.deepEqual(categories(result), []);
  });

  it("截断 varint：两侧都拒绝但错误类别不同 => reject_category_mismatch", () => {
    const result = compare(new Uint8Array([0x80]), strictParser, lenientParser);
    assert.equal(categories(result)[0], "reject_category_mismatch");
    assert.equal(result.divergences[0].left?.error?.category, "malformed");
    assert.equal(result.divergences[0].right?.error?.category, "truncated");
  });
});

describe("语义值工具", () => {
  it("对象键顺序不同仍语义相等，数组顺序不同不相等", () => {
    assert.equal(semanticEqual({ a: 1, b: 2 }, { b: 2, a: 1 }), true);
    assert.equal(semanticEqual([1, 2], [2, 1]), false);
    assert.equal(
      semanticEqual(new Uint8Array([1, 2]), new Uint8Array([1, 2])),
      true,
    );
    assert.equal(semanticEqual(10n, 10n), true);
  });

  it("stableStringify 对对象键顺序稳定", () => {
    assert.equal(
      stableStringify({ a: 1, b: 2 }),
      stableStringify({ b: 2, a: 1 }),
    );
  });
});
