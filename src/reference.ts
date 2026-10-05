// 参考求值器：每次从头按作用域遍历整棵树，不使用任何缓存。
// 与增量求值器共享同一套纯函数（环境、类型/常量规则、诊断消息），
// 输出同样的 key -> 序列化值 快照，用于对拍验证增量结果的正确性。
// 仅实现顺序可见性（语言规范）；'block' 可见性只是增量求值器的循环演示开关。

import type { NodeId, Program, Stmt, TypeName } from './ast.ts';
import {
  UNKNOWN, binopConst, binopType, diag, extendEnv, lookupEnv, msg,
  notConst, notType, serializeAttr,
} from './lang.ts';
import type { Binding, ConstValue, Diagnostic, Env } from './lang.ts';

interface ExprResult {
  type: TypeName;
  const: ConstValue;
  errors: Diagnostic[];
}
interface StmtResult {
  envOut: Env;
  errors: Diagnostic[];
}

export function evaluateReference(prog: Program): Map<string, string> {
  const out = new Map<string, string>();
  const rec = (id: NodeId, attr: string, v: unknown): void => {
    out.set(`${id}.${attr}`, serializeAttr(attr, v));
  };

  function walkExpr(id: NodeId, env: Env): ExprResult {
    const node = prog.node(id);
    rec(id, 'envIn', env);
    let type: TypeName;
    let cnst: ConstValue;
    const errors: Diagnostic[] = [];
    switch (node.kind) {
      case 'num':
        type = 'int';
        cnst = node.value;
        break;
      case 'bool':
        type = 'bool';
        cnst = node.value;
        break;
      case 'ref': {
        const b: Binding | null = lookupEnv(env, node.name) ?? null;
        rec(id, 'binding', b);
        if (b === null) errors.push(diag(id, 'binding', msg.unbound(node.name)));
        type = b ? b.type : 'error';
        cnst = b ? b.const : UNKNOWN;
        break;
      }
      case 'bin': {
        const l = walkExpr(node.left.id, env);
        const r = walkExpr(node.right.id, env);
        type = binopType(node.op, l.type, r.type);
        cnst = type === 'error' ? UNKNOWN : binopConst(node.op, l.const, r.const);
        if (l.type !== 'error' && r.type !== 'error' && type === 'error') {
          errors.push(diag(id, 'type', msg.binMismatch(node.op, l.type, r.type)));
        }
        errors.push(...l.errors, ...r.errors);
        break;
      }
      case 'not': {
        const e = walkExpr(node.expr.id, env);
        type = notType(e.type);
        cnst = type === 'error' ? UNKNOWN : notConst(e.const);
        if (e.type !== 'error' && type === 'error') {
          errors.push(diag(id, 'type', msg.notMismatch(e.type)));
        }
        errors.push(...e.errors);
        break;
      }
      default:
        throw new Error(`reference: not an expression node ${node.kind}`);
    }
    rec(id, 'type', type);
    rec(id, 'const', cnst);
    rec(id, 'errors', errors);
    return { type, const: cnst, errors };
  }

  function walkStmt(stmt: Stmt, envIn: Env, blockId: NodeId): StmtResult {
    const id = stmt.id;
    rec(id, 'envIn', envIn);
    let envOut: Env = envIn;
    const errors: Diagnostic[] = [];
    switch (stmt.kind) {
      case 'const': {
        const e = walkExpr(stmt.expr.id, envIn);
        const prev = lookupEnv(envIn, stmt.name);
        if (prev && prev.blockId === blockId) {
          errors.push(diag(id, 'envOut', msg.dupDecl(stmt.name, blockId, prev.declId)));
        }
        errors.push(...e.errors);
        envOut = extendEnv(envIn, {
          name: stmt.name, declId: id, blockId,
          type: e.type, const: e.const, kind: 'const',
        });
        break;
      }
      case 'func': {
        const prev = lookupEnv(envIn, stmt.name);
        if (prev && prev.blockId === blockId) {
          errors.push(diag(id, 'envOut', msg.dupDecl(stmt.name, blockId, prev.declId)));
        }
        const seen = new Set<string>();
        for (const p of stmt.params) {
          if (seen.has(p.name)) errors.push(diag(id, 'envOut', msg.dupParam(p.name, stmt.name)));
          seen.add(p.name);
        }
        let bodyEnv: Env = envIn;
        for (const p of stmt.params) {
          bodyEnv = extendEnv(bodyEnv, {
            name: p.name, declId: p.id, blockId: stmt.body.id,
            type: p.ptype, const: UNKNOWN, kind: 'param',
          });
        }
        const b = walkBlock(stmt.body, bodyEnv);
        errors.push(...b.errors);
        envOut = extendEnv(envIn, {
          name: stmt.name, declId: id, blockId, type: 'func', const: UNKNOWN, kind: 'func',
        });
        break;
      }
      case 'block': {
        const b = walkBlock(stmt, envIn);
        errors.push(...b.errors);
        envOut = envIn; // 块内声明不外泄
        break;
      }
      case 'expr': {
        const e = walkExpr(stmt.expr.id, envIn);
        errors.push(...e.errors);
        envOut = envIn;
        break;
      }
    }
    rec(id, 'envOut', envOut);
    rec(id, 'errors', errors);
    return { envOut, errors };
  }

  function walkBlock(block: { id: NodeId; stmts: Stmt[] }, envIn: Env): { errors: Diagnostic[] } {
    rec(block.id, 'envIn', envIn);
    rec(block.id, 'envOut', envIn); // 块作为语句时 envOut = envIn
    let env = envIn;
    const errors: Diagnostic[] = [];
    for (const s of block.stmts) {
      const r = walkStmt(s, env, block.id);
      env = r.envOut;
      errors.push(...r.errors);
    }
    rec(block.id, 'errors', errors);
    return { errors };
  }

  if (!prog.root) throw new Error('program has no root block');
  walkBlock(prog.root, null);
  return out;
}
