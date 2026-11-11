/**
 * 类型树差分引擎。
 *
 * 最小路径原则：任一节点一旦产生分歧即停止向下展开；
 * 无序集合中每个无匹配键各产生一条 cardinality_mismatch，互不嵌套。
 */

import { semanticEqual, stableStringify } from "./value.ts";
import type {
  ChildNode,
  Divergence,
  PathSegment,
  SideEvidence,
  TypeNode,
} from "./types.ts";

const fingerprintCache = new WeakMap<TypeNode, string>();

/** 深度语义指纹：类型 + 语义值 + 子树（含重复字段名与其原始位置信息之外的全部语义）。 */
export function fingerprint(node: TypeNode): string {
  const cached = fingerprintCache.get(node);
  if (cached !== undefined) return cached;

  const childPart = node.children
    ? `[${node.children
        .map((child) => {
          const namePart = child.name === undefined ? "" : `${JSON.stringify(child.name)}:`;
          return `${namePart}${fingerprint(child.node)}`;
        })
        .join(",")}]`
    : "";
  const result = `{"t":${JSON.stringify(node.type)},"v":${stableStringify(node.value)},"c":${childPart}}`;
  fingerprintCache.set(node, result);
  return result;
}

function nodeEvidence(node: TypeNode): SideEvidence {
  return {
    type: node.type,
    value: node.value,
    encoding: node.encoding,
    range: node.range,
  };
}

function segment(child: ChildNode, index: number): PathSegment {
  return { name: child.name, index, type: child.node.type };
}

function sameRange(a: TypeNode, b: TypeNode): boolean {
  return a.range.start === b.range.start && a.range.end === b.range.end;
}

function makeDivergence(
  category: Divergence["category"],
  severity: Divergence["severity"],
  semantic: boolean,
  path: PathSegment[],
  left: SideEvidence | null,
  right: SideEvidence | null,
  detail: string,
): Divergence {
  return { category, severity, semantic, path, left, right, detail };
}

function isComposite(node: TypeNode): boolean {
  return node.children !== undefined;
}

/**
 * 比较两棵（已被双方接受的）类型树。
 * 返回的分歧均绑定最小路径与各自原始字节区间。
 */
export function diffTrees(left: TypeNode, right: TypeNode): Divergence[] {
  return diffNodes(left, right, [], true);
}

function diffNodes(
  left: TypeNode,
  right: TypeNode,
  path: PathSegment[],
  checkOwnRange: boolean,
): Divergence[] {
  if (left.type !== right.type) {
    return [
      makeDivergence(
        "type_mismatch",
        "semantic",
        true,
        path,
        nodeEvidence(left),
        nodeEvidence(right),
        `type differs: ${left.type} vs ${right.type}`,
      ),
    ];
  }

  if (isComposite(left) !== isComposite(right)) {
    return [
      makeDivergence(
        "type_mismatch",
        "semantic",
        true,
        path,
        nodeEvidence(left),
        nodeEvidence(right),
        `structure differs: composite vs leaf for type ${left.type}`,
      ),
    ];
  }

  if (!isComposite(left)) {
    return diffLeaves(left, right, path, checkOwnRange);
  }
  return diffComposites(left, right, path);
}

function diffLeaves(
  left: TypeNode,
  right: TypeNode,
  path: PathSegment[],
  checkOwnRange: boolean,
): Divergence[] {
  if (!semanticEqual(left.value, right.value)) {
    return [
      makeDivergence(
        "value_mismatch",
        "semantic",
        true,
        path,
        nodeEvidence(left),
        nodeEvidence(right),
 "semantic values differ",
      ),
    ];
  }

  const out: Divergence[] = [];
  if (
    left.encoding !== undefined &&
    right.encoding !== undefined &&
    left.encoding !== right.encoding
  ) {
    out.push(
      makeDivergence(
        "encoding_mismatch",
        "informational",
        false,
        path,
        nodeEvidence(left),
        nodeEvidence(right),
        `equal value with different wire encoding: ${left.encoding} vs ${right.encoding}`,
      ),
    );
  }
  if (checkOwnRange && !sameRange(left, right)) {
    out.push(
      makeDivergence(
        "range_mismatch",
        "informational",
        false,
        path,
        nodeEvidence(left),
        nodeEvidence(right),
        "byte ranges differ for semantically equal nodes",
      ),
    );
  }
  return out;
}

function diffComposites(
  left: TypeNode,
  right: TypeNode,
  path: PathSegment[],
): Divergence[] {
  const leftChildren = left.children ?? [];
  const rightChildren = right.children ?? [];

  // 只有双方都显式标记 ordered === false，才允许重排。
  const unordered = left.ordered === false && right.ordered === false;
  if (!unordered && leftChildren.length !== rightChildren.length) {
    return [
      makeDivergence(
        "cardinality_mismatch",
        "semantic",
        true,
        path,
        nodeEvidence(left),
        nodeEvidence(right),
        `child count differs: ${leftChildren.length} vs ${rightChildren.length}`,
      ),
    ];
  }
  return unordered
    ? diffUnordered(leftChildren, rightChildren, path)
    : diffOrdered(leftChildren, rightChildren, path);
}

function diffOrdered(
  leftChildren: ChildNode[],
  rightChildren: ChildNode[],
  path: PathSegment[],
): Divergence[] {
  for (let index = 0; index < leftChildren.length; index += 1) {
    const leftChild = leftChildren[index];
    const rightChild = rightChildren[index];
    const childPath = [...path, segment(leftChild, index)];

    if (fingerprint(leftChild.node) === fingerprint(rightChild.node)) {
      const inner = diffNodes(leftChild.node, rightChild.node, childPath, true);
      if (inner.length > 0) return inner;
      continue;
    }

    // 位置对不上但键的多重集完全相同：纯粹是顺序分歧，归并为一条 order_mismatch。
    if (sameFingerprintMultiset(leftChildren, rightChildren)) {
      return [
        makeDivergence(
          "order_mismatch",
          "semantic",
          true,
          childPath,
          nodeEvidence(leftChild.node),
          nodeEvidence(rightChild.node),
          "ordered collection contains the same elements in a different order",
        ),
      ];
    }

    // 内容本身不同：在该最小位置展开为 type/value 分歧，然后停止。
    return diffNodes(leftChild.node, rightChild.node, childPath, true);
  }
  return [];
}

function sameFingerprintMultiset(left: ChildNode[], right: ChildNode[]): boolean {
  const counts = new Map<string, number>();
  for (const child of left) {
    const key = fingerprint(child.node);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  for (const child of right) {
    const key = fingerprint(child.node);
    const remaining = counts.get(key);
    if (remaining === undefined || remaining === 0) return false;
    counts.set(key, remaining - 1);
  }
  return true;
}

function diffUnordered(
  leftChildren: ChildNode[],
  rightChildren: ChildNode[],
  path: PathSegment[],
): Divergence[] {
  const leftByKey = groupByKey(leftChildren);
  const rightByKey = groupByKey(rightChildren);
  const allKeys = new Set([...leftByKey.keys(), ...rightByKey.keys()]);
  const out: Divergence[] = [];

  for (const key of [...allKeys].sort()) {
    const leftGroup = leftByKey.get(key) ?? [];
    const rightGroup = rightByKey.get(key) ?? [];
    const common = Math.min(leftGroup.length, rightGroup.length);

    // 多重集基数差异：多出的元素逐个报告，证据只落在实际持有它的一侧。
    if (leftGroup.length !== rightGroup.length) {
      const extraOnLeft = leftGroup.length > rightGroup.length;
      const extraSide = extraOnLeft ? leftGroup : rightGroup;
      for (let i = common; i < extraSide.length; i += 1) {
        const extraEntry = extraSide[i];
        out.push(
          makeDivergence(
            "cardinality_mismatch",
            "semantic",
            true,
            [...path, segment(extraEntry.child, extraEntry.index)],
            extraOnLeft ? nodeEvidence(extraEntry.child.node) : null,
            extraOnLeft ? null : nodeEvidence(extraEntry.child.node),
            `unordered collection cardinality differs for key ${key}: ${leftGroup.length} vs ${rightGroup.length}`,
          ),
        );
      }
    }

    // 同指纹元素按出现次序确定性配对，继续向内比较编码与内部结构。
    // 重排层级本身不再比较节点区间（位置本来就允许不同）。
    for (let i = 0; i < common; i += 1) {
      const leftEntry = leftGroup[i];
      const rightEntry = rightGroup[i];
      const leftChild = leftEntry.child;
      const rightChild = rightEntry.child;
      const childPath = [
        ...path,
        segment(leftChild, leftEntry.index),
      ];
      const inner = diffNodes(leftChild.node, rightChild.node, childPath, false);
      out.push(...inner);
    }
  }
  return out;
}

interface IndexedChild {
  child: ChildNode;
  index: number;
}

function groupByKey(children: ChildNode[]): Map<string, IndexedChild[]> {
  const groups = new Map<string, IndexedChild[]>();
  children.forEach((child, index) => {
    const key = fingerprint(child.node);
    const group = groups.get(key);
    if (group) group.push({ child, index });
    else groups.set(key, [{ child, index }]);
  });
  return groups;
}
