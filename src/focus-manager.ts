// FocusManager — 用虚拟节点树管理弹层栈的焦点。
//
// 核心不变量：
// 1. 关闭子层后，焦点回到该层的触发节点；
// 2. 触发节点已删除/禁用/不可聚焦时，沿虚拟树向上找最近仍可聚焦的祖先；
// 3. 祖先也不可用时，退到父层（再往下）的第一个可聚焦项；
// 4. 关闭是异步的（等待过渡动画），期间若有更新的开/关/聚焦动作，
//    过期的恢复操作不得抢走新层的焦点（restoreToken 纪元机制）。
//
// 注意：本文件只使用“可擦除”的 TypeScript 语法（类型注解、接口、类型别名），
// 不使用 enum / namespace / 参数属性 / 非空断言，以便 scripts/strip-types.mjs
// 能在无 tsc 的环境下把它转换为等价 JS 运行测试。

export interface NodeSpec {
  id: string;
  focusable?: boolean;
  disabled?: boolean;
  children?: NodeSpec[];
}

export interface LayerSpec {
  id: string;
  triggerId?: string | null;
  root: NodeSpec;
}

export interface LayerOpenedEvent {
  type: 'layer:opened';
  layerId: string;
}

export interface LayerClosingEvent {
  type: 'layer:closing';
  layerId: string;
}

export interface LayerClosedEvent {
  type: 'layer:closed';
  layerId: string;
}

export interface FocusChangeEvent {
  type: 'focus:change';
  from: string | null;
  to: string | null;
  reason: string;
}

export interface NodeRemovedEvent {
  type: 'node:removed';
  id: string;
}

export interface NodeDisabledEvent {
  type: 'node:disabled';
  id: string;
  disabled: boolean;
}

export type FocusEvent =
  | LayerOpenedEvent
  | LayerClosingEvent
  | LayerClosedEvent
  | FocusChangeEvent
  | NodeRemovedEvent
  | NodeDisabledEvent;

interface VNode {
  id: string;
  parentId: string | null;
  layerId: string;
  focusable: boolean;
  disabled: boolean;
  deleted: boolean;
  children: string[];
}

interface Layer {
  id: string;
  triggerId: string | null;
  rootId: string;
}

type TransitionFn = () => Promise<void> | void;

type EventListener = (event: FocusEvent) => void;

export interface FocusManagerOptions {
  transition?: TransitionFn;
}

export class FocusManager {
  private nodes: Map<string, VNode> = new Map();
  private layers: Layer[] = [];
  private focusedId: string | null = null;
  private restoreToken: number = 0;
  private transition: TransitionFn;
  private listeners: EventListener[] = [];
  readonly log: FocusEvent[] = [];

  constructor(options: FocusManagerOptions = {}) {
    this.transition = options.transition ?? (() => undefined);
  }

  get focused(): string | null {
    return this.focusedId;
  }

  get stack(): string[] {
    return this.layers.map((layer) => layer.id);
  }

  onEvent(listener: EventListener): () => void {
    this.listeners.push(listener);
    return () => {
      const index = this.listeners.indexOf(listener);
      if (index !== -1) this.listeners.splice(index, 1);
    };
  }

  openLayer(spec: LayerSpec): void {
    if (this.layers.some((layer) => layer.id === spec.id)) {
      throw new Error(`duplicate layer id: ${spec.id}`);
    }
    const rootId = this.registerSubtree(spec.root, null, spec.id);
    this.layers.push({ id: spec.id, triggerId: spec.triggerId ?? null, rootId });
    // 新层打开使所有未完成的异步恢复过期
    this.restoreToken++;
    this.emit({ type: 'layer:opened', layerId: spec.id });
    const first = this.firstFocusableInLayer(this.layers[this.layers.length - 1]);
    if (first !== null) this.setFocus(first, 'layer-open');
  }

  async closeLayer(layerId: string): Promise<void> {
    const index = this.layers.findIndex((layer) => layer.id === layerId);
    if (index === -1) return;
    // 目标层及其当前所有子层级联关闭
    const closing = this.layers.slice(index);
    const closingIds = new Set(closing.map((layer) => layer.id));
    const token = ++this.restoreToken;
    for (const layer of [...closing].reverse()) {
      this.emit({ type: 'layer:closing', layerId: layer.id });
    }
    await this.transition();
    this.layers = this.layers.filter((layer) => !closingIds.has(layer.id));
    for (const layer of [...closing].reverse()) {
      this.markSubtreeDeleted(layer.rootId);
      this.emit({ type: 'layer:closed', layerId: layer.id });
    }
    if (token !== this.restoreToken) {
      // 关闭期间发生了更新的开/关/聚焦：不得抢焦点，
      // 仅兜底处理焦点滞留在已关闭层内的情况。
      this.refocusIfStranded();
      return;
    }
    this.restoreFocus(closing[0].triggerId, index - 1);
  }

  removeNode(id: string): void {
    const node = this.nodes.get(id);
    if (node === undefined || node.deleted) return;
    this.markSubtreeDeleted(id);
    if (node.parentId !== null) {
      const parent = this.nodes.get(node.parentId);
      if (parent !== undefined) {
        const at = parent.children.indexOf(id);
        if (at !== -1) parent.children.splice(at, 1);
      }
    }
    this.emit({ type: 'node:removed', id });
    if (this.focusedId !== null) {
      const focused = this.nodes.get(this.focusedId);
      if (focused === undefined || focused.deleted) {
        this.refocusFrom(node.parentId, 'node-removed');
      }
    }
  }

  setDisabled(id: string, disabled: boolean): void {
    const node = this.nodes.get(id);
    if (node === undefined || node.deleted || node.disabled === disabled) return;
    node.disabled = disabled;
    this.emit({ type: 'node:disabled', id, disabled });
    if (disabled && this.focusedId === id) {
      this.refocusFrom(node.parentId, 'node-disabled');
    }
  }

  focus(id: string): boolean {
    const node = this.nodes.get(id);
    if (node === undefined || !this.isFocusable(node)) return false;
    this.setFocus(id, 'api');
    return true;
  }

  // 触发节点 -> 最近可聚焦祖先 -> 自 fallbackIndex 向下的层首项 -> null
  private restoreFocus(triggerId: string | null, fallbackIndex: number): void {
    let current = triggerId;
    while (current !== null) {
      const node = this.nodes.get(current);
      if (node === undefined) break;
      if (this.isFocusable(node)) {
        this.setFocus(node.id, 'restore');
        return;
      }
      current = node.parentId;
    }
    const start = Math.min(fallbackIndex, this.layers.length - 1);
    for (let index = start; index >= 0; index--) {
      const first = this.firstFocusableInLayer(this.layers[index]);
      if (first !== null) {
        this.setFocus(first, 'restore');
        return;
      }
    }
    this.setFocus(null, 'restore');
  }

  private refocusFrom(startId: string | null, reason: string): void {
    let current = startId;
    while (current !== null) {
      const node = this.nodes.get(current);
      if (node === undefined) break;
      if (this.isFocusable(node)) {
        this.setFocus(node.id, reason);
        return;
      }
      current = node.parentId;
    }
    for (let index = this.layers.length - 1; index >= 0; index--) {
      const first = this.firstFocusableInLayer(this.layers[index]);
      if (first !== null) {
        this.setFocus(first, reason);
        return;
      }
    }
    this.setFocus(null, reason);
  }

  private refocusIfStranded(): void {
    if (this.focusedId === null) return;
    const focused = this.nodes.get(this.focusedId);
    if (focused !== undefined && !focused.deleted) return;
    this.refocusFrom(null, 'layer-close');
  }

  private setFocus(id: string | null, reason: string): void {
    if (id === this.focusedId) return;
    // 任何真实的焦点移动都使未完成的异步恢复过期
    this.restoreToken++;
    const from = this.focusedId;
    this.focusedId = id;
    this.emit({ type: 'focus:change', from, to: id, reason });
  }

  private emit(event: FocusEvent): void {
    this.log.push(event);
    for (const listener of [...this.listeners]) listener(event);
  }

  private isFocusable(node: VNode): boolean {
    return !node.deleted && !node.disabled && node.focusable;
  }

  private firstFocusableInLayer(layer: Layer): string | null {
    return this.walk(layer.rootId);
  }

  private walk(id: string): string | null {
    const node = this.nodes.get(id);
    if (node === undefined || node.deleted) return null;
    if (this.isFocusable(node)) return node.id;
    for (const childId of node.children) {
      const found = this.walk(childId);
      if (found !== null) return found;
    }
    return null;
  }

  private registerSubtree(spec: NodeSpec, parentId: string | null, layerId: string): string {
    if (this.nodes.has(spec.id)) {
      throw new Error(`duplicate node id: ${spec.id}`);
    }
    const node: VNode = {
      id: spec.id,
      parentId,
      layerId,
      focusable: spec.focusable !== false,
      disabled: spec.disabled === true,
      deleted: false,
      children: [],
    };
    this.nodes.set(spec.id, node);
    for (const child of spec.children ?? []) {
      node.children.push(this.registerSubtree(child, spec.id, layerId));
    }
    return spec.id;
  }

  private markSubtreeDeleted(id: string): void {
    const node = this.nodes.get(id);
    if (node === undefined || node.deleted) return;
    node.deleted = true;
    for (const childId of node.children) {
      this.markSubtreeDeleted(childId);
    }
  }
}
