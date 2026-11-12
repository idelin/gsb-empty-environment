/**
 * 嵌套浏览上下文（框架树）上的权限策略。
 *
 * 每一个框架节点是树中的一层，携带：
 *  - origin:           该框架当前文档的来源（可能是不透明来源）；
 *  - declared:         本层文档声明的“功能 -> 允许列表”映射；
 *  - container:        父框架通过嵌入容器（iframe allow 属性）施加给本层的“功能 -> 允许列表”映射；
 *                     根节点没有父容器，该字段被忽略。
 *
 * 有效策略沿根 -> 当前节点路径逐层求交。任意一层缺失某功能的声明，等价于该层对该功能
 * 不做收紧（允许任意具名来源）。通配只代表该层允许域内的具名来源，无法解除上层限制。
 */

import type { Allowlist } from "./allowlist.ts";
import { ALLOW_ALL, ALLOW_BASE, intersect, matches } from "./allowlist.ts";
import type { Origin } from "./origin.ts";

/** 功能名到允许列表的映射。 */
export type PolicyMap = Readonly<Record<string, Allowlist>>;

export interface FrameNodeConfig {
  readonly id?: string;
  readonly origin: Origin;
  /** 本层文档声明的策略。 */
  readonly declared?: PolicyMap;
  /** 父嵌入容器（allow 属性）施加的策略；根节点忽略此项。 */
  readonly container?: PolicyMap;
  readonly children?: ReadonlyArray<FrameNodeConfig>;
}

export interface FrameNode {
  readonly id: string;
  readonly origin: Origin;
  readonly declared: PolicyMap;
  readonly container: PolicyMap;
  readonly parent: FrameNode | null;
  readonly children: ReadonlyArray<FrameNode>;
}

interface MutableNode {
  id: string;
  origin: Origin;
  declared: PolicyMap;
  container: PolicyMap;
  parent: MutableNode | null;
  children: MutableNode[];
}

let nodeCounter = 0;

function nextId(): string {
  nodeCounter += 1;
  return `frame-${nodeCounter}`;
}

function buildNode(config: FrameNodeConfig, parent: MutableNode | null): MutableNode {
  const node: MutableNode = {
    id: config.id ?? nextId(),
    origin: config.origin,
    declared: config.declared ?? {},
    container: config.container ?? {},
    parent,
    children: [],
  };
  node.children = (config.children ?? []).map((child) => buildNode(child, node));
  return node;
}

/** 由配置构建一棵框架树，返回根节点。节点之间的 parent 链接会自动接线。 */
export function buildTree(config: FrameNodeConfig): FrameNode {
  return buildNode(config, null) as FrameNode;
}

function asMutable(node: FrameNode): MutableNode {
  return node as unknown as MutableNode;
}

/** 收集树中出现过的全部功能名。 */
export function allFeatures(root: FrameNode): string[] {
  const features = new Set<string>();
  walk(root, (node) => {
    for (const feature of Object.keys(node.declared)) features.add(feature);
    for (const feature of Object.keys(node.container)) features.add(feature);
  });
  return [...features];
}

/** 深度优先遍历。 */
export function walk(root: FrameNode, visit: (node: FrameNode) => void): void {
  visit(root);
  for (const child of root.children) walk(child, visit);
}

/** 按 id 查找节点，未找到返回 undefined。 */
export function findNode(root: FrameNode, id: string): FrameNode | undefined {
  let found: FrameNode | undefined;
  walk(root, (node) => {
    if (node.id === id) found = node;
  });
  return found;
}

/** 返回从根到该节点的路径（含两端），依据节点的 parent 链接向上回溯。 */
export function pathTo(root: FrameNode, target: FrameNode): FrameNode[] {
  void root;
  const reversed: FrameNode[] = [];
  let current: FrameNode | null = target;
  while (current !== null) {
    reversed.push(current);
    current = current.parent;
  }
  return reversed.reverse();
}

function layerAllowlist(node: FrameNode, feature: string): Allowlist {
  const own = node.declared[feature];
  if (node.parent === null) {
    return own ?? ALLOW_ALL;
  }
  const container = node.container[feature] ?? ALLOW_ALL;
  return own === undefined ? container : intersect(own, container);
}

/**
 * 计算某节点对某功能的有效允许列表：根到该节点各层允许列表的交集。
 * 缺失声明视为该层不收紧（ALLOW_ALL）；折叠以 ALLOW_BASE 为单位元，因此
 * 未在任何层出现的功能返回 ALLOW_ALL，且首层显式携带的不透明令牌不会被提前丢弃。
 */
export function effectiveAllowlist(root: FrameNode, node: FrameNode, feature: string): Allowlist {
  const path = pathTo(root, node);
  let effective = ALLOW_BASE;
  for (const layer of path) {
    effective = intersect(effective, layerAllowlist(layer, feature));
  }
  return effective;
}

/**
 * 判断某节点上的某功能对给定请求来源是否放行。
 * 不透明来源仅当自身令牌在有效允许列表中显式出现时才放行。
 */
export function isAllowed(
  root: FrameNode,
  node: FrameNode,
  feature: string,
  requester: Origin,
): boolean {
  return matches(effectiveAllowlist(root, node, feature), requester);
}

/** 计算某节点的完整有效策略（树中出现过的每个功能各一条）。 */
export function effectivePolicy(root: FrameNode, node: FrameNode): PolicyMap {
  const result: Record<string, Allowlist> = {};
  for (const feature of allFeatures(root)) {
    result[feature] = effectiveAllowlist(root, node, feature);
  }
  return result;
}

function cloneTree(root: MutableNode): MutableNode {
  const clone: MutableNode = {
    id: root.id,
    origin: root.origin,
    declared: root.declared,
    container: root.container,
    parent: null,
    children: [],
  };
  clone.children = root.children.map((child) => {
    const childClone = cloneTree(child);
    childClone.parent = clone;
    return childClone;
  });
  return clone;
}

export interface NavigateOptions {
  /** 新文档的策略声明；缺省为空声明。 */
  readonly declared?: PolicyMap;
  /** 新文档的父容器策略；缺省保留原容器策略。 */
  readonly container?: PolicyMap;
}

/**
 * 导航某个浏览上下文到新文档。
 *
 * 导航会替换该节点的来源与文档声明，并销毁、重建其整个子树（旧子框架全部消失）。
 * 返回一棵全新的树；旧树对象保持不变，旧节点上已经算出的有效策略快照随之失效，
 * 以此表达“旧能力失效”。
 */
export function navigate(
    root: FrameNode,
    targetId: string,
    newOrigin: Origin,
    options: NavigateOptions = {},
): FrameNode {
  const cloned = cloneTree(asMutable(root));
  const target = findMutable(cloned, targetId);
  if (target === undefined) {
    throw new Error(`navigate: node not found: ${targetId}`);
  }
  target.origin = newOrigin;
  target.declared = options.declared ?? {};
  if (options.container !== undefined && target.parent !== null) {
    target.container = options.container;
  }
  for (const child of target.children) {
    child.parent = null;
  }
  target.children = [];
  return cloned as FrameNode;
}

export interface MigrateOptions {
  /** 迁移后该节点相对新父级的容器策略；缺省保留原容器策略。 */
  readonly container?: PolicyMap;
}

/**
 * 把子框架（含其子树）迁移到新的父框架下，模拟 iframe 在文档间移动。
 *
 * 被迁移节点自身的来源与文档声明保持不变，但其有效策略按“新祖先路径 + 容器策略”
 * 重新求交，因此可能被新祖先收紧。返回一棵全新的树；旧树保持不变。
 */
export function migrateSubtree(
    root: FrameNode,
    nodeId: string,
    newParentId: string,
    options: MigrateOptions = {},
): FrameNode {
  if (nodeId === root.id) {
    throw new Error("migrateSubtree: the root frame cannot be migrated");
  }
  const cloned = cloneTree(asMutable(root));
  const node = findMutable(cloned, nodeId);
  const newParent = findMutable(cloned, newParentId);
  if (node === undefined) {
    throw new Error(`migrateSubtree: node not found: ${nodeId}`);
  }
  if (newParent === undefined) {
    throw new Error(`migrateSubtree: new parent not found: ${newParentId}`);
  }
  if (isAncestor(node, newParent)) {
    throw new Error("migrateSubtree: cannot migrate a node into its own descendant");
  }
  const oldParent = node.parent;
  if (oldParent === null) {
    throw new Error("migrateSubtree: node has no parent");
  }
  oldParent.children = oldParent.children.filter((child) => child.id !== nodeId);
  node.parent = newParent;
  if (options.container !== undefined) {
    node.container = options.container;
  }
  newParent.children.push(node);
  return cloned as FrameNode;
}

function findMutable(root: MutableNode, id: string): MutableNode | undefined {
  if (root.id === id) return root;
  for (const child of root.children) {
    const found = findMutable(child, id);
    if (found !== undefined) return found;
  }
  return undefined;
}

function isAncestor(maybeAncestor: MutableNode, node: MutableNode): boolean {
  let current: MutableNode | null = node;
  while (current !== null) {
    if (current.id === maybeAncestor.id) return true;
    current = current.parent;
  }
  return false;
}
