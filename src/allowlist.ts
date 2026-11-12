/**
 * 功能允许列表（allowlist）。
 *
 * 一个允许列表由两部分组成：
 *  - wildcard: 是否在“当前层”允许任意具名来源（对应策略语法里的 `*`）；
 *  - explicit: 显式点名的来源集合（具名来源或不透明来源令牌）。
 *
 * 关键规则：
 *  - 通配 `*` 只覆盖具名来源，永远不会匹配不透明来源；不透明来源必须凭自身令牌显式出现；
 *  - 允许列表是“某一层”的声明，它本身不携带任何祖先信息，跨层收紧由 intersection 完成。
 */

import type { Origin } from "./origin.ts";
import { originKey } from "./origin.ts";

export interface Allowlist {
  readonly wildcard: boolean;
  readonly explicit: ReadonlySet<string>;
}

/** 通配：允许当前层任意具名来源，不含不透明来源。 */
export const ALLOW_ALL: Allowlist = {
  wildcard: true,
  explicit: new Set<string>(),
};

/**
 * 逐层折叠的单位元：对具名来源通配，且不预先排除任何来源。
 * 与 ALLOW_ALL 不同，它不参与显式集合的过滤，因此不会在第一层丢掉
 * 由后续层显式携带的不透明来源令牌。
 */
export const ALLOW_BASE: unique symbol = Symbol("ALLOW_BASE");

export type AllowlistOrBase = Allowlist | typeof ALLOW_BASE;

/** 全拒绝（对应策略语法里的 `'none'`）。 */
export const ALLOW_NONE: Allowlist = {
  wildcard: false,
  explicit: new Set<string>(),
};

/** 仅允许给定来源（不使用通配）。 */
export function allowOnly(...origins: Origin[]): Allowlist {
  return { wildcard: false, explicit: new Set(origins.map(originKey)) };
}

/** 在通配基础上再显式点名若干来源（常用于额外纳入不透明来源）。 */
export function allowAllPlus(...origins: Origin[]): Allowlist {
  return { wildcard: true, explicit: new Set(origins.map(originKey)) };
}

/** 自定义允许列表。 */
export function allowlist(wildcard: boolean, origins: Origin[] = []): Allowlist {
  return { wildcard, explicit: new Set(origins.map(originKey)) };
}

/**
 * 判断来源是否被该允许列表接受。
 * 不透明来源只看显式集合中的自身令牌，通配对其无效。
 */
export function matches(list: Allowlist, value: Origin): boolean {
  const key = originKey(value);
  if (value.kind === "opaque") {
    return list.explicit.has(key);
  }
  return list.wildcard || list.explicit.has(key);
}

/**
 * 两层允许列表的交集。
 *
 * 对两侧显式集合键的并集中的每个来源，只有它在两侧分别都被接受时才保留：
 *  - 具名来源：某层为通配即接受，否则需在该层显式出现；
 *  - 不透明来源：通配无效，必须在该层显式携带自身令牌。
 *
 * 该定义满足结合律，因此沿根到叶路径折叠的结果与折叠顺序无关。
 */
export function intersect(left: AllowlistOrBase, right: AllowlistOrBase): Allowlist {
  if (left === ALLOW_BASE) return right === ALLOW_BASE ? { wildcard: true, explicit: new Set() } : right;
  if (right === ALLOW_BASE) return left;
  const explicit = new Set<string>();
  for (const key of new Set([...left.explicit, ...right.explicit])) {
    const source = keyToOrigin(key);
    if (matches(left, source) && matches(right, source)) {
      explicit.add(key);
    }
  }
  return { wildcard: left.wildcard && right.wildcard, explicit };
}

/** 把规范化键还原为可用于 matches 的最小来源结构。 */
function keyToOrigin(key: string): Origin {
  if (key.startsWith("opaque:")) {
    return { kind: "opaque", token: key.slice("opaque:".length) };
  }
  return { kind: "origin", value: key.slice("origin:".length) };
}
