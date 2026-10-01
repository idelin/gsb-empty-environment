/**
 * 细粒度 UI 更新引擎。
 *
 * 核心思路：模板中的动态部分（文本 / 属性）以「绑定」形式声明。
 * 挂载时引擎在响应式上下文中求值每个绑定，自动收集其依赖的 Signal，
 * 并建立 绑定 → 目标节点 id 的直接映射。
 *
 * Signal 变更时，只重算依赖它的绑定并对比新旧标量值，
 * 产出最小补丁（UpdateTextNode / SetAttribute / RemoveAttribute）。
 * 全程不遍历、不对比组件树 —— 没有 VDOM Diff。
 *
 * 每个绑定持有自己的 last 影子值，因此无论补丁是否已被应用，
 * 引擎都能正确增量地产出后续补丁。
 */

import { withObserver } from './signal.ts';
import type { Patch, VElement, VNode } from './vnode.ts';

/** 文本绑定占位：t(() => '欢迎，' + user.name()) */
export interface TextBinding {
  readonly __kind: 'text-binding';
  readonly compute: () => string;
}

/** 属性绑定占位：attr(() => user.name())，返回 null/undefined/false 表示移除属性 */
export interface AttrBinding {
  readonly __kind: 'attr-binding';
  readonly compute: () => string | null | undefined | false;
}

export function t(compute: () => string): TextBinding {
  return { __kind: 'text-binding', compute };
}

export function attr(
  compute: () => string | null | undefined | false,
): AttrBinding {
  return { __kind: 'attr-binding', compute };
}

type AttrValue = string | AttrBinding;
type Child = VNode | TextBinding | string;

/** 模板 DSL：h('p', { class: 'x' }, '静态文本', t(...), h(...)) */
export function h(
  tag: string,
  attrs: Record<string, AttrValue> | null,
  ...children: Child[]
): VElement {
  return {
    type: 'element',
    id: -1, // 挂载时分配
    tag,
    attrs: attrs as Record<string, string>,
    children: children as VNode[],
  };
}

export interface MountResult {
  root: VElement;
  /** 节点 id → 节点：补丁应用时的直接寻址表 */
  index: Map<number, VNode>;
  /** 挂载后引擎产出的全部补丁（挂载期初值写入不算补丁） */
  patches: Patch[];
  /** 统计：绑定求值总次数（用于证明更新期无全树遍历/对比） */
  stats: { evaluations: number };
}

export function mount(template: VElement): MountResult {
  let nextId = 1;
  const index = new Map<number, VNode>();
  const patches: Patch[] = [];
  const stats = { evaluations: 0 };
  /** 挂载期为 true：初值直接写入节点，不产出补丁 */
  let mounting = true;

  const mountTextBinding = (tb: TextBinding): VNode => {
    const node: VNode = { type: 'text', id: nextId++, text: '' };
    index.set(node.id, node);
    if (node.type !== 'text') throw new Error('unreachable');
    let last = '';

    const reevaluate = () => {
      stats.evaluations++;
      // reevaluate 即订阅身份：求值期间的 Signal 读取会自动登记依赖
      const next = String(withObserver(reevaluate, tb.compute));
      if (mounting) {
        node.text = next; // 初值直写，不产生补丁
      } else if (next !== last) {
        // 精准补丁：只携带目标节点 id 与新文本
        patches.push({ op: 'UpdateTextNode', id: node.id, text: next });
      }
      last = next;
    };
    reevaluate();
    return node;
  };

  const mountAttrBinding = (
    el: VElement,
    name: string,
    ab: AttrBinding,
  ): void => {
    let last: string | null = null;

    const reevaluate = () => {
      stats.evaluations++;
      const raw = withObserver(reevaluate, ab.compute);
      const next =
        raw === null || raw === undefined || raw === false
          ? null
          : String(raw);
      if (mounting) {
        if (next !== null) el.attrs[name] = next; // 初值直写
      } else if (next !== last) {
        if (next === null) {
          patches.push({ op: 'RemoveAttribute', id: el.id, name });
        } else {
          patches.push({ op: 'SetAttribute', id: el.id, name, value: next });
        }
      }
      last = next;
    };
    reevaluate();
  };

  const mountNode = (vnode: VNode): VNode => {
    if (vnode.type === 'text') {
      vnode.id = nextId++;
      index.set(vnode.id, vnode);
      return vnode;
    }
    vnode.id = nextId++;
    index.set(vnode.id, vnode);

    // 属性：静态值直写，AttrBinding 转为属性绑定
    const rawAttrs = (vnode.attrs ?? {}) as Record<string, AttrValue>;
    vnode.attrs = {};
    for (const [name, value] of Object.entries(rawAttrs)) {
      if (
        typeof value === 'object' &&
        value !== null &&
        value.__kind === 'attr-binding'
      ) {
        mountAttrBinding(vnode, name, value);
      } else {
        vnode.attrs[name] = value as string;
      }
    }

    vnode.children = vnode.children.map((child) => {
      if (typeof child === 'string') {
        const node: VNode = { type: 'text', id: nextId++, text: child };
        index.set(node.id, node);
        return node;
      }
      if ((child as TextBinding).__kind === 'text-binding') {
        return mountTextBinding(child as TextBinding);
      }
      return mountNode(child as VNode);
    });
    return vnode;
  };

  const root = mountNode(template) as VElement;
  mounting = false;
  return { root, index, patches, stats };
}
