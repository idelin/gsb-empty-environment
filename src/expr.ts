import { getPath, isJSONObject, parsePath, type JSONValue } from './json.ts';
import { PlanError, type Diagnostic } from './errors.ts';
import {
  tNever,
  tNumber,
  tString,
  tUnknown,
  unionOf,
  walkType,
  type Type,
} from './types.ts';

/** 计算表达式：字面量、字段引用、四则运算、函数调用、动态字段读取。 */
export type Expr =
  | { op: 'lit'; value: JSONValue }
  | { op: 'field'; path: string }
  | { op: 'add' | 'sub' | 'mul' | 'div'; left: Expr; right: Expr }
  | { op: 'call'; fn: string; args: Expr[] }
  | { op: 'readDynamic'; key: Expr };

export interface FnSig {
  args: Type[];
  ret: Type;
  impl: (...xs: JSONValue[]) => JSONValue;
}

/** 已知函数表：不在表中的函数一律报错，绝不推断为 any。 */
export const KNOWN_FNS: Record<string, FnSig> = {
  strlen: { args: [tString], ret: tNumber, impl: (s) => (s as string).length },
  upper: { args: [tString], ret: tString, impl: (s) => (s as string).toUpperCase() },
  lower: { args: [tString], ret: tString, impl: (s) => (s as string).toLowerCase() },
  abs: { args: [tNumber], ret: tNumber, impl: (x) => Math.abs(x as number) },
  concat: {
    args: [tString, tString],
    ret: tString,
    impl: (a, b) => (a as string) + (b as string),
  },
};

export interface TypeCtx {
  stepId: string;
  diagnostics: Diagnostic[];
  reads: Set<string>;
}

function isNumeric(t: Type): boolean {
  return (
    t.kind === 'number' ||
    t.kind === 'unknown' ||
    (t.kind === 'literal' && typeof t.value === 'number')
  );
}

function isStringy(t: Type): boolean {
  return (
    t.kind === 'string' ||
    t.kind === 'unknown' ||
    (t.kind === 'literal' && typeof t.value === 'string')
  );
}

function assignableTo(got: Type, want: Type): boolean {
  if (want.kind === 'unknown' || got.kind === 'unknown') return true;
  if (got.kind === 'literal') {
    const v = got.value;
    switch (want.kind) {
      case 'number':
        return typeof v === 'number';
      case 'string':
        return typeof v === 'string';
      case 'boolean':
        return typeof v === 'boolean';
      case 'null':
        return v === null;
      default:
        return false;
    }
  }
  if (got.kind === 'union') return got.options.every((o) => assignableTo(o, want));
  return got.kind === want.kind;
}

/** 表达式静态定型：记录诊断与读取集合，返回类型。 */
export function typeExpr(expr: Expr, schema: Type, ctx: TypeCtx): Type {
  switch (expr.op) {
    case 'lit':
      return { kind: 'literal', value: expr.value };
    case 'field': {
      ctx.reads.add(expr.path);
      const r = walkType(schema, parsePath(expr.path));
      if (!r) {
        ctx.diagnostics.push({
          severity: 'error',
          stepId: ctx.stepId,
          path: expr.path,
          message: `读取不存在或已删除的字段 ${expr.path}`,
        });
        return tUnknown;
      }
      if (r.optional) {
        ctx.diagnostics.push({
          severity: 'warning',
          stepId: ctx.stepId,
          path: expr.path,
          message: `读取可缺失字段 ${expr.path}（分支合流后仅单侧提供）`,
        });
      }
      return r.type;
    }
    case 'add':
    case 'sub':
    case 'mul':
    case 'div': {
      const lt = typeExpr(expr.left, schema, ctx);
      const rt = typeExpr(expr.right, schema, ctx);
      if (lt.kind === 'never' || rt.kind === 'never') return tNever;
      if (!isNumeric(lt) || !isNumeric(rt)) {
        ctx.diagnostics.push({
          severity: 'error',
          stepId: ctx.stepId,
          message: `算术运算 ${expr.op} 需要数值操作数`,
        });
        return tUnknown;
      }
      if (lt.kind === 'unknown' || rt.kind === 'unknown') {
        ctx.diagnostics.push({
          severity: 'warning',
          stepId: ctx.stepId,
          message: `算术运算 ${expr.op} 的操作数类型未知，结果保守记为 unknown`,
        });
        return tUnknown;
      }
      return tNumber;
    }
    case 'call': {
      const sig = KNOWN_FNS[expr.fn];
      if (!sig) {
        for (const a of expr.args) typeExpr(a, schema, ctx);
        ctx.diagnostics.push({
          severity: 'error',
          stepId: ctx.stepId,
          message: `未知函数 '${expr.fn}'：不推断为 any，结果按 unknown 处理`,
        });
        return tUnknown;
      }
      const argTypes = expr.args.map((a) => typeExpr(a, schema, ctx));
      argTypes.forEach((got, i) => {
        const want = sig.args[i];
        if (!want) {
          ctx.diagnostics.push({
            severity: 'error',
            stepId: ctx.stepId,
            message: `函数 '${expr.fn}' 参数过多`,
          });
          return;
        }
        if (got.kind === 'unknown') {
          ctx.diagnostics.push({
            severity: 'warning',
            stepId: ctx.stepId,
            message: `函数 '${expr.fn}' 第 ${i + 1} 个参数类型未知，保守通过`,
          });
        } else if (!assignableTo(got, want)) {
          ctx.diagnostics.push({
            severity: 'error',
            stepId: ctx.stepId,
            message: `函数 '${expr.fn}' 第 ${i + 1} 个参数类型不匹配`,
          });
        }
      });
      return sig.ret;
    }
    case 'readDynamic': {
      const kt = typeExpr(expr.key, schema, ctx);
      if (!isStringy(kt)) {
        ctx.diagnostics.push({
          severity: 'error',
          stepId: ctx.stepId,
          message: `动态字段读取的键必须是字符串`,
        });
      }
      if (schema.kind !== 'object') {
        ctx.diagnostics.push({
          severity: 'warning',
          stepId: ctx.stepId,
          message: `动态字段读取作用于非对象，结果保守记为 unknown`,
        });
        return tUnknown;
      }
      // 保守建模：键未知 ⇒ 可能读取任意字段 ⇒ 依赖全部字段，结果为全部字段类型的联合。
      for (const name of Object.keys(schema.fields)) ctx.reads.add('$.' + name);
      ctx.diagnostics.push({
        severity: 'info',
        stepId: ctx.stepId,
        message: `动态字段读取：保守建模为全部字段类型的联合，且运行时可缺失`,
      });
      const all = Object.values(schema.fields);
      if (all.length === 0) return tUnknown;
      return unionOf(all.map((f) => f.type));
    }
  }
}

/** 表达式运行求值：缺失字段抛出带路径的 PlanError。 */
export function evalExpr(expr: Expr, obj: JSONValue, stepId: string): JSONValue {
  switch (expr.op) {
    case 'lit':
      return expr.value;
    case 'field': {
      const r = getPath(obj, parsePath(expr.path));
      if (!r.found) throw new PlanError(`运行时缺失字段 ${expr.path}`, stepId, expr.path);
      return r.value!;
    }
    case 'add':
    case 'sub':
    case 'mul':
    case 'div': {
      const l = evalExpr(expr.left, obj, stepId);
      const r = evalExpr(expr.right, obj, stepId);
      if (typeof l !== 'number' || typeof r !== 'number') {
        throw new PlanError(`算术运算 ${expr.op} 的操作数非数值`, stepId);
      }
      switch (expr.op) {
        case 'add':
          return l + r;
        case 'sub':
          return l - r;
        case 'mul':
          return l * r;
        case 'div':
          return l / r;
      }
      break;
    }
    case 'call': {
      const sig = KNOWN_FNS[expr.fn];
      if (!sig) throw new PlanError(`未知函数 '${expr.fn}'`, stepId);
      return sig.impl(...expr.args.map((a) => evalExpr(a, obj, stepId)));
    }
    case 'readDynamic': {
      const k = evalExpr(expr.key, obj, stepId);
      if (typeof k !== 'string') throw new PlanError(`动态读取的键非字符串`, stepId);
      if (!isJSONObject(obj) || !(k in obj)) {
        throw new PlanError(`运行时缺失字段 $.${k}`, stepId, `$.${k}`);
      }
      return obj[k]!;
    }
  }
  throw new PlanError(`无法求值的表达式`, stepId);
}

/** 收集表达式引用的字段路径。 */
export function exprReads(expr: Expr, out: Set<string> = new Set()): Set<string> {
  switch (expr.op) {
    case 'lit':
      break;
    case 'field':
      out.add(expr.path);
      break;
    case 'add':
    case 'sub':
    case 'mul':
    case 'div':
      exprReads(expr.left, out);
      exprReads(expr.right, out);
      break;
    case 'call':
      for (const a of expr.args) exprReads(a, out);
      break;
    case 'readDynamic':
      exprReads(expr.key, out);
      break;
  }
  return out;
}
