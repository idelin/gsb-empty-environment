// 语言语义：把“名称绑定 / 常量值 / 类型”定义为 AST 上的有限属性。
//
// 属性一览（I=继承属性, S=综合属性）：
//   envIn    I  语句/表达式：进入该节点时可见的环境（声明从其后语句起可见）
//   envOut   S  语句：该语句之后可见的环境（const/func 会扩展；块不外泄）
//   blockEnv S  块：块内所有声明一次可见（仅 visibility='block'，用于演示循环）
//   binding  S  引用：名字解析到的绑定（未绑定时也记录对作用域的依赖）
//   type     S  表达式：int / bool / func / error
//   const    S  表达式：编译期常量值（UNKNOWN 表示非常量）
//   errors   S  所有节点：诊断列表，自底向上聚合
//
// 顺序可见性下属性依赖严格按“前序语句 -> 后序语句”单向流动，语法层面无环；
// visibility='block' 是教学对照开关，可制造真实的跨属性循环（见 cycle 测试）。

import { AttrEvaluator } from './evaluator.ts';
import type { NodeId } from './evaluator.ts';
import { isExpr, isStmt } from './ast.ts';
import type { AnyNode, BinOp, BlockStmt, Program, TypeName } from './ast.ts';

export const UNKNOWN: unique symbol = Symbol('UNKNOWN');
export type ConstValue = number | boolean | typeof UNKNOWN;

export interface Binding {
  name: string;
  declId: NodeId;
  blockId: NodeId; // 声明所在的块：同块重复声明检测的依据
  type: TypeName;
  const: ConstValue;
  kind: 'const' | 'func' | 'param';
}

/** 环境 = 不可变绑定链表（内层绑定在前）。 */
export interface EnvNode {
  parent: EnvNode | null;
  binding: Binding;
}
export type Env = EnvNode | null;

export function extendEnv(parent: Env, binding: Binding): Env {
  return { parent, binding };
}
export function lookupEnv(env: Env, name: string): Binding | undefined {
  let cur = env;
  while (cur) {
    if (cur.binding.name === name) return cur.binding;
    cur = cur.parent;
  }
  return undefined;
}

export interface Diagnostic {
  node: NodeId;
  attr: string; // 出错的属性名：错误位置 = 节点ID + 属性名
  message: string;
}
export function diag(node: NodeId, attr: string, message: string): Diagnostic {
  return { node, attr, message };
}

// 诊断消息集中定义，保证增量求值器与参考求值器逐字一致。
export const msg = {
  unbound: (name: string) => `unbound variable '${name}'`,
  binMismatch: (op: BinOp, lt: TypeName, rt: TypeName) =>
    `type mismatch: '${op}' cannot be applied to ${lt} and ${rt}`,
  notMismatch: (t: TypeName) => `type mismatch: 'not' cannot be applied to ${t}`,
  dupDecl: (name: string, blockId: NodeId, firstDeclId: NodeId) =>
    `duplicate declaration of '${name}' in block ${blockId} (first declared at ${firstDeclId})`,
  dupParam: (name: string, fname: string) =>
    `duplicate parameter '${name}' of function '${fname}'`,
};

export function binopType(op: BinOp, lt: TypeName, rt: TypeName): TypeName {
  if (lt === 'error' || rt === 'error') return 'error';
  switch (op) {
    case '+': case '-': case '*':
      return lt === 'int' && rt === 'int' ? 'int' : 'error';
    case '<':
      return lt === 'int' && rt === 'int' ? 'bool' : 'error';
    case '==':
      return lt === rt ? 'bool' : 'error';
    case 'and': case 'or':
      return lt === 'bool' && rt === 'bool' ? 'bool' : 'error';
  }
}
export function notType(t: TypeName): TypeName {
  return t === 'bool' ? 'bool' : 'error';
}
export function binopConst(op: BinOp, lv: ConstValue, rv: ConstValue): ConstValue {
  if (lv === UNKNOWN || rv === UNKNOWN) return UNKNOWN;
  switch (op) {
    case '+': return (lv as number) + (rv as number);
    case '-': return (lv as number) - (rv as number);
    case '*': return (lv as number) * (rv as number);
    case '<': return (lv as number) < (rv as number);
    case '==': return lv === rv;
    case 'and': return (lv as boolean) && (rv as boolean);
    case 'or': return (lv as boolean) || (rv as boolean);
  }
}
export function notConst(v: ConstValue): ConstValue {
  return v === UNKNOWN ? UNKNOWN : !(v as boolean);
}

export interface LangOptions {
  /** 'sequential'（默认，声明从其后语句起可见）| 'block'（块内全部声明一次可见，可成环）。 */
  visibility?: 'sequential' | 'block';
}

function mustNode(prog: Program, id: NodeId): AnyNode {
  return prog.node(id);
}

export function registerLanguage(ev: AttrEvaluator, prog: Program, opts: LangOptions = {}): void {
  const visibility = opts.visibility ?? 'sequential';

  const parentBlockIdOf = (id: NodeId): NodeId => {
    const p = prog.parents.get(id);
    if (p === undefined) throw new Error(`node ${id} has no parent block`);
    return p;
  };

  // ---- envIn（继承）----
  ev.registerRule('envIn', (id) => {
    const node = mustNode(prog, id);
    if (isExpr(node)) {
      return ev.get(prog.enclosingStmtId(id), 'envIn') as Env;
    }
    if (!isStmt(node)) throw new Error(`envIn not defined for ${node.kind}`);
    const pid = prog.parents.get(id);
    if (pid === undefined) return null; // 根块：空环境
    const parent = mustNode(prog, pid);
    if (parent.kind === 'func') {
      // 函数体块：外层环境 + 参数绑定（参数属于函数体块的作用域）
      let env = ev.get(pid, 'envIn') as Env;
      for (const p of parent.params) {
        env = extendEnv(env, {
          name: p.name, declId: p.id, blockId: parent.body.id,
          type: p.ptype, const: UNKNOWN, kind: 'param',
        });
      }
      return env;
    }
    if (parent.kind !== 'block') throw new Error(`unexpected parent ${parent.kind}`);
    if (visibility === 'block') return ev.get(pid, 'blockEnv');
    const idx = parent.stmts.findIndex((s) => s.id === id);
    if (idx <= 0) return ev.get(pid, 'envIn') as Env;
    return ev.get(parent.stmts[idx - 1].id, 'envOut') as Env;
  });

  // ---- envOut（综合）----
  ev.registerRule('envOut', (id) => {
    const node = mustNode(prog, id);
    if (node.kind === 'const') {
      const envIn = ev.get(id, 'envIn') as Env;
      return extendEnv(envIn, {
        name: node.name, declId: id, blockId: parentBlockIdOf(id),
        type: ev.get(node.expr.id, 'type') as TypeName,
        const: ev.get(node.expr.id, 'const') as ConstValue,
        kind: 'const',
      });
    }
    if (node.kind === 'func') {
      const envIn = ev.get(id, 'envIn') as Env;
      return extendEnv(envIn, {
        name: node.name, declId: id, blockId: parentBlockIdOf(id),
        type: 'func', const: UNKNOWN, kind: 'func',
      });
    }
    if (node.kind === 'block' || node.kind === 'expr') {
      return ev.get(id, 'envIn') as Env; // 块与表达式语句不改变环境
    }
    throw new Error(`envOut not defined for ${node.kind}`);
  });

  // ---- blockEnv（综合；仅 visibility='block' 时使用）----
  ev.registerRule('blockEnv', (id) => {
    const node = mustNode(prog, id);
    if (node.kind !== 'block') throw new Error('blockEnv only defined for blocks');
    let env = ev.get(id, 'envIn') as Env;
    for (const s of (node as BlockStmt).stmts) {
      if (s.kind === 'const') {
        env = extendEnv(env, {
          name: s.name, declId: s.id, blockId: id,
          type: ev.get(s.expr.id, 'type') as TypeName,
          const: ev.get(s.expr.id, 'const') as ConstValue,
          kind: 'const',
        });
      } else if (s.kind === 'func') {
        env = extendEnv(env, {
          name: s.name, declId: s.id, blockId: id, type: 'func', const: UNKNOWN, kind: 'func',
        });
      }
    }
    return env;
  });

  // ---- binding（综合）：未绑定时同样记录对作用域 envIn 的依赖（via=lookup:<名>）----
  ev.registerRule('binding', (id) => {
    const node = mustNode(prog, id);
    if (node.kind !== 'ref') throw new Error('binding only defined for refs');
    const env = ev.get(id, 'envIn', `lookup:${node.name}`) as Env;
    return lookupEnv(env, node.name) ?? null;
  });

  // ---- type（综合）----
  ev.registerRule('type', (id) => {
    const node = mustNode(prog, id);
    switch (node.kind) {
      case 'num': return 'int';
      case 'bool': return 'bool';
      case 'ref': {
        const b = ev.get(id, 'binding') as Binding | null;
        return b ? b.type : 'error';
      }
      case 'bin':
        return binopType(node.op,
          ev.get(node.left.id, 'type') as TypeName,
          ev.get(node.right.id, 'type') as TypeName);
      case 'not':
        return notType(ev.get(node.expr.id, 'type') as TypeName);
      default:
        throw new Error(`type not defined for ${node.kind}`);
    }
  });

  // ---- const（综合）----
  ev.registerRule('const', (id) => {
    const node = mustNode(prog, id);
    switch (node.kind) {
      case 'num': return node.value;
      case 'bool': return node.value;
      case 'ref': {
        const b = ev.get(id, 'binding') as Binding | null;
        return b ? b.const : UNKNOWN;
      }
      case 'bin': {
        if ((ev.get(id, 'type') as TypeName) === 'error') return UNKNOWN;
        return binopConst(node.op,
          ev.get(node.left.id, 'const') as ConstValue,
          ev.get(node.right.id, 'const') as ConstValue);
      }
      case 'not': {
        if ((ev.get(id, 'type') as TypeName) === 'error') return UNKNOWN;
        return notConst(ev.get(node.expr.id, 'const') as ConstValue);
      }
      default:
        throw new Error(`const not defined for ${node.kind}`);
    }
  });

  // ---- errors（综合，自底向上聚合）----
  ev.registerRule('errors', (id) => {
    const node = mustNode(prog, id);
    const out: Diagnostic[] = [];
    switch (node.kind) {
      case 'num':
      case 'bool':
        break;
      case 'ref': {
        if ((ev.get(id, 'binding') as Binding | null) === null) {
          out.push(diag(id, 'binding', msg.unbound(node.name)));
        }
        break;
      }
      case 'bin': {
        const lt = ev.get(node.left.id, 'type') as TypeName;
        const rt = ev.get(node.right.id, 'type') as TypeName;
        if (lt !== 'error' && rt !== 'error' && binopType(node.op, lt, rt) === 'error') {
          out.push(diag(id, 'type', msg.binMismatch(node.op, lt, rt)));
        }
        out.push(...(ev.get(node.left.id, 'errors') as Diagnostic[]));
        out.push(...(ev.get(node.right.id, 'errors') as Diagnostic[]));
        break;
      }
      case 'not': {
        const t = ev.get(node.expr.id, 'type') as TypeName;
        if (t !== 'error' && notType(t) === 'error') {
          out.push(diag(id, 'type', msg.notMismatch(t)));
        }
        out.push(...(ev.get(node.expr.id, 'errors') as Diagnostic[]));
        break;
      }
      case 'const': {
        const prev = lookupEnv(ev.get(id, 'envIn') as Env, node.name);
        if (prev && prev.blockId === parentBlockIdOf(id)) {
          out.push(diag(id, 'envOut', msg.dupDecl(node.name, parentBlockIdOf(id), prev.declId)));
        }
        out.push(...(ev.get(node.expr.id, 'errors') as Diagnostic[]));
        break;
      }
      case 'func': {
        const prev = lookupEnv(ev.get(id, 'envIn') as Env, node.name);
        if (prev && prev.blockId === parentBlockIdOf(id)) {
          out.push(diag(id, 'envOut', msg.dupDecl(node.name, parentBlockIdOf(id), prev.declId)));
        }
        const seen = new Set<string>();
        for (const p of node.params) {
          if (seen.has(p.name)) out.push(diag(id, 'envOut', msg.dupParam(p.name, node.name)));
          seen.add(p.name);
        }
        out.push(...(ev.get(node.body.id, 'errors') as Diagnostic[]));
        break;
      }
      case 'block': {
        for (const s of node.stmts) out.push(...(ev.get(s.id, 'errors') as Diagnostic[]));
        break;
      }
      case 'expr': {
        out.push(...(ev.get(node.expr.id, 'errors') as Diagnostic[]));
        break;
      }
      default:
        throw new Error(`errors not defined for ${(node as AnyNode).kind}`);
    }
    return out;
  });
}

// ---------- 序列化与快照（增量结果与参考结果对拍用） ----------

export function serializeConst(v: ConstValue): string {
  return v === UNKNOWN ? '?' : String(v);
}
export function serializeBinding(b: Binding): string {
  return `${b.name}@${b.declId}:${b.type}=${serializeConst(b.const)}#${b.blockId}`;
}
export function serializeEnv(env: Env): string {
  if (env === null) return '∅';
  const parts: string[] = [];
  let cur: Env = env;
  while (cur) {
    parts.push(serializeBinding(cur.binding));
    cur = cur.parent;
  }
  return parts.join(' <- ');
}
export function serializeErrors(errs: Diagnostic[]): string {
  if (errs.length === 0) return '(none)';
  return errs.map((d) => `${d.node}.${d.attr}: ${d.message}`).join('; ');
}

export function serializeAttr(attr: string, v: unknown): string {
  switch (attr) {
    case 'envIn':
    case 'envOut':
    case 'blockEnv':
      return serializeEnv(v as Env);
    case 'binding':
      return v === null ? 'unbound' : serializeBinding(v as Binding);
    case 'const':
      return serializeConst(v as ConstValue);
    case 'errors':
      return serializeErrors(v as Diagnostic[]);
    default:
      return String(v);
  }
}

/** 每种节点参与对拍的属性集合（与参考求值器保持一致）。 */
export function attrsFor(node: AnyNode): string[] {
  switch (node.kind) {
    case 'block':
    case 'const':
    case 'func':
    case 'expr':
      return ['envIn', 'envOut', 'errors'];
    case 'num':
    case 'bool':
    case 'bin':
    case 'not':
      return ['envIn', 'type', 'const', 'errors'];
    case 'ref':
      return ['envIn', 'binding', 'type', 'const', 'errors'];
    case 'param':
      return [];
  }
}

/** 触发增量求值器计算整棵树，并返回 key -> 序列化值 的快照。 */
export function snapshotIncremental(prog: Program, ev: AttrEvaluator): Map<string, string> {
  const out = new Map<string, string>();
  const walk = (id: NodeId): void => {
    const node = prog.node(id);
    for (const attr of attrsFor(node)) {
      out.set(`${id}.${attr}`, serializeAttr(attr, ev.get(id, attr)));
    }
    for (const c of prog.childrenOf(id)) walk(c);
  };
  if (prog.root) walk(prog.root.id);
  return out;
}
