// ---------------------------------------------------------------------------
// 常量折叠优化器。
//
// 安全性论证（对任意环境保持：值、首个错误、变量访问序列完全一致）：
//  1. 纯常量子树折叠为常量 / error 节点 —— 常量求值无可观察效果，error 节点
//     留在原位，到点才抛错，求值顺序与首个错误不变。
//  2. 短路折叠（左操作数为常量）：被跳过的右子树在原语义下也绝不会被求值，
//     因此删除它不会丢失任何变量读取或错误。
//  3. 条件折叠（条件为常量）：未被选中的分支同理可删。
//  4. 不做任何会删除变量读取或改变值类型的化简（如 x*0、x+0 一律保留）。
//
// 每个输出节点带 src：来源对应（该节点由哪些源节点折叠而来）。
// ---------------------------------------------------------------------------

import { computeBin, truthy, type Node, type Value } from './ast.ts';

export type OptNode = Node & { src?: number[] };

export function fold(node: Node): OptNode {
  switch (node.kind) {
    case 'const':
      return { ...node, src: [node.id] };
    case 'var':
      return { ...node, src: [node.id] };
    case 'error':
      return { ...node, src: [node.id] };
    case 'bin': {
      const l = fold(node.left);
      const r = fold(node.right);
      if (l.kind === 'const' && r.kind === 'const') {
        const res = computeBin(node.op, l.value, r.value);
        const src = [node.id, ...(l.src ?? []), ...(r.src ?? [])];
        if (!res.ok) {
          // 常量除零：保留一个 error 节点在原位置，到求值至此才抛错。
          return { kind: 'error', id: node.id, src };
        }
        return { kind: 'const', id: node.id, value: res.value, src };
      }
      return { ...node, left: l, right: r, src: [node.id] };
    }
    case 'logic': {
      const l = fold(node.left);
      if (l.kind === 'const') {
        const dropped = [node.id, ...(l.src ?? [])];
        if (node.op === '&&') {
          // 假 && e => 假（e 永不求值，可整体删除，含其中的错误）
          // 真 && e => e
          return truthy(l.value)
            ? absorb(fold(node.right), dropped)
            : { kind: 'const', id: node.id, value: l.value, src: dropped };
        }
        // 真 || e => 真；假 || e => e
        return truthy(l.value)
          ? { kind: 'const', id: node.id, value: l.value, src: dropped }
          : absorb(fold(node.right), dropped);
      }
      return { ...node, left: l, right: fold(node.right), src: [node.id] };
    }
    case 'cond': {
      const c = fold(node.cond);
      if (c.kind === 'const') {
        const dropped = [node.id, ...(c.src ?? [])];
        // 条件已知：只保留会执行的分支；未执行分支（含其中错误）整体删除。
        return absorb(fold(truthy(c.value) ? node.then : node.els), dropped);
      }
      return { ...node, cond: c, then: fold(node.then), els: fold(node.els), src: [node.id] };
    }
  }
}

// 把被折叠掉的祖先节点 ID 并入幸存节点的来源对应中。
function absorb(node: OptNode, extra: number[]): OptNode {
  return { ...node, src: [...extra, ...(node.src ?? [])] };
}

export function srcOf(node: OptNode): number[] {
  return node.src ?? [node.id];
}
