/**
 * 接口兼容检查器（结构化子类型判定）
 *
 * 类型系统（有限类型）：
 *   - 原始类型：string / number / boolean / null
 *   - 记录：字段可有「可缺省」标记（键可不存在），与「可空」（union [T, null]，键必须存在）严格区分
 *   - 联合：有限分支
 *   - 函数：参数逆变、返回值协变
 *   - 命名引用：表达递归类型，比较通过「成对访问状态」终止（共归纳）
 *
 * 判定方向：checkReplacement(新, 旧) —— 新接口能否替代旧接口（新 ≤ 旧，Liskov 可替换）。
 * 输出：不兼容路径 + 最小值形状反例（属于新类型、但不属于旧类型的值形状）。
 */

export type PrimitiveName = "string" | "number" | "boolean" | "null";

export type TypeNode =
  | { kind: "primitive"; name: PrimitiveName }
  | { kind: "record"; fields: Record<string, Field> }
  | { kind: "union"; options: TypeNode[] }
  | { kind: "function"; params: TypeNode[]; returns: TypeNode }
  | { kind: "ref"; name: string };

export interface Field {
  type: TypeNode;
  /** 可缺省：键可以不存在。与可空（union [T, null]，键必须存在、值可为 null）严格区分。 */
  optional?: boolean;
}

export type TypeEnv = Record<string, TypeNode>;

/** 最小值形状：反例只包含说明不兼容所需的最少部分 */
export type Shape =
  | { kind: "atom"; value: string | number | boolean | null }
  | { kind: "record"; fields: Record<string, Shape> } // 可缺省字段省略
  | { kind: "fn-return"; returns: Shape } // 函数值：返回了旧接口不允许的值
  | { kind: "fn-call"; argIndex: number; argument: Shape } // 函数值：被旧接口允许、新接口拒绝的实参调用
  | { kind: "rec"; name: string }; // 递归截断标记

export interface Failure {
  /** 不兼容路径，如 $.user.name、$.onEvent(参数1·逆变).msg、$.list.next<#1>.head */
  path: string;
  reason: string;
  /** 属于新类型（逆变位置则为旧参数类型）但不属于旧类型的最小值形状 */
  counterexample: Shape;
}

export interface CheckResult {
  compatible: boolean;
  failures: Failure[];
}

// ---------------------------------------------------------------------------
// 类型构造辅助
// ---------------------------------------------------------------------------

export const T = {
  str: { kind: "primitive", name: "string" } as TypeNode,
  num: { kind: "primitive", name: "number" } as TypeNode,
  bool: { kind: "primitive", name: "boolean" } as TypeNode,
  nil: { kind: "primitive", name: "null" } as TypeNode,
  /** 可空：键必须存在，值可为 null（与可缺省不同） */
  nullable: (t: TypeNode): TypeNode => ({ kind: "union", options: [t, T.nil] }),
  union: (...options: TypeNode[]): TypeNode => ({ kind: "union", options }),
  fn: (params: TypeNode[], returns: TypeNode): TypeNode => ({ kind: "function", params, returns }),
  ref: (name: string): TypeNode => ({ kind: "ref", name }),
  /** 记录；optional 列出可缺省字段名 */
  rec: (fields: Record<string, TypeNode>, optional: string[] = []): TypeNode => ({
    kind: "record",
    fields: Object.fromEntries(
      Object.entries(fields).map(([k, t]) => [k, { type: t, optional: optional.includes(k) }]),
    ),
  }),
};

// ---------------------------------------------------------------------------
// 内部工具
// ---------------------------------------------------------------------------

const nodeIds = new WeakMap<object, number>();
let nextNodeId = 0;

function nodeId(t: TypeNode): number {
  let id = nodeIds.get(t);
  if (id === undefined) {
    id = nextNodeId++;
    nodeIds.set(t, id);
  }
  return id;
}

/** 展开命名引用；检测无结构的循环别名（如 type A = A） */
function resolve(t: TypeNode, env: TypeEnv): TypeNode {
  const seen = new Set<string>();
  while (t.kind === "ref") {
    if (seen.has(t.name)) throw new Error(`循环类型别名且无任何结构: ${t.name}`);
    seen.add(t.name);
    const target = env[t.name];
    if (!target) throw new Error(`未定义的类型引用: ${t.name}`);
    t = target;
  }
  return t;
}

function shapeCost(s: Shape): number {
  switch (s.kind) {
    case "atom":
    case "rec":
      return 1;
    case "record":
      return 1 + Object.values(s.fields).reduce((acc, f) => acc + shapeCost(f), 0);
    case "fn-return":
      return 1 + shapeCost(s.returns);
    case "fn-call":
      return 1 + shapeCost(s.argument);
  }
}

/** 类型的最小值形状：记录只含必填字段；联合取代价最小分支；递归引用截断为标记 */
export function minimalShape(t: TypeNode, env: TypeEnv = {}, expanding: Set<string> = new Set()): Shape {
  switch (t.kind) {
    case "primitive":
      return {
        kind: "atom",
        value: t.name === "string" ? "" : t.name === "number" ? 0 : t.name === "boolean" ? false : null,
      };
    case "record": {
      const fields: Record<string, Shape> = {};
      for (const [k, f] of Object.entries(t.fields)) {
        if (!f.optional) fields[k] = minimalShape(f.type, env, expanding);
      }
      return { kind: "record", fields };
    }
    case "union": {
      if (t.options.length === 0) return { kind: "atom", value: null };
      let best: Shape | null = null;
      for (const opt of t.options) {
        const s = minimalShape(opt, env, expanding);
        if (best === null || shapeCost(s) < shapeCost(best)) best = s;
      }
      return best as Shape;
    }
    case "function":
      return { kind: "fn-return", returns: minimalShape(t.returns, env, expanding) };
    case "ref": {
      if (expanding.has(t.name)) return { kind: "rec", name: t.name };
      expanding.add(t.name);
      const s = minimalShape(resolve(t, env), env, expanding);
      expanding.delete(t.name);
      return s;
    }
  }
}

export function renderShape(s: Shape): string {
  switch (s.kind) {
    case "atom":
      return JSON.stringify(s.value);
    case "record": {
      const parts = Object.entries(s.fields).map(([k, v]) => `${k}: ${renderShape(v)}`);
      return parts.length === 0 ? "{}" : `{ ${parts.join(", ")} }`;
    }
    case "fn-return":
      return `fn(...) => ${renderShape(s.returns)}`;
    case "fn-call":
      return `fn(参数${s.argIndex + 1} = ${renderShape(s.argument)}) 被调用`;
    case "rec":
      return `<递归 ${s.name} …>`;
  }
}

// ---------------------------------------------------------------------------
// 子类型判定：sub(新, 旧) 返回空数组表示兼容
// ---------------------------------------------------------------------------

function sub(n: TypeNode, o: TypeNode, env: TypeEnv, path: string, visited: Set<string>): Failure[] {
  const rn = resolve(n, env);
  const ro = resolve(o, env);

  // 成对访问状态：同一对类型已在比较中，按共归纳假设兼容 —— 递归类型在此终止
  const pairKey = `${nodeId(rn)}<=${nodeId(ro)}`;
  if (visited.has(pairKey)) return [];
  visited.add(pairKey);

  // 新类型是联合：每个分支都必须 ≤ 旧类型
  if (rn.kind === "union") {
    const failures: Failure[] = [];
    rn.options.forEach((opt, i) => {
      failures.push(...sub(opt, ro, env, `${path}<#${i + 1}>`, visited));
    });
    return failures;
  }

  // 旧类型是联合：新类型只需 ≤ 其中一个分支（逐分支尝试，失败回滚访问状态）
  if (ro.kind === "union") {
    for (const opt of ro.options) {
      const snapshot = new Set(visited);
      if (sub(rn, opt, env, path, visited).length === 0) return [];
      visited.clear();
      for (const k of snapshot) visited.add(k);
    }
    return [
      {
        path,
        reason: `不匹配旧联合类型的任何分支（共 ${ro.options.length} 个）`,
        counterexample: minimalShape(rn, env),
      },
    ];
  }

  if (rn.kind !== ro.kind) {
    return [
      {
        path,
            reason: `类型种类不同：${rn.kind} vs ${ro.kind}`,
        counterexample: minimalShape(rn, env),
      },
    ];
  }

  if (rn.kind === "primitive" && ro.kind === "primitive") {
    return rn.name === ro.name
      ? []
      : [
          {
            path,
            reason: `原始类型不兼容：${rn.name} 不能当作 ${ro.name} 使用`,
            counterexample: minimalShape(rn, env),
          },
        ];
  }

  if (rn.kind === "record" && ro.kind === "record") {
    const failures: Failure[] = [];
    const base = minimalShape(rn, env); // 新类型的最小记录形状（可缺省字段已省略）
    if (base.kind !== "record") throw new Error("unreachable");
    for (const [k, of] of Object.entries(ro.fields)) {
      const nf = rn.fields[k];
      const fieldPath = `${path}.${k}`;
      if (!nf) {
        if (!of.optional) {
          failures.push({
            path: fieldPath,
            reason: "值形状缺少此必填字段（键不存在）",
            counterexample: base,
          });
        }
        continue; // 新类型多出的字段不影响（宽度子类型）
      }
      if (nf.optional && !of.optional) {
        failures.push({
          path: fieldPath,
          reason: "一侧允许键缺省，另一侧要求键必须存在（可缺省 ≠ 可空）",
          counterexample: base, // 最小形状天然省略该键，即为反例
        });
        continue;
      }
      // 字段协变：把子反例嵌入新类型的最小记录形状
      for (const f of sub(nf.type, of.type, env, fieldPath, visited)) {
        failures.push({
          ...f,
          counterexample: { kind: "record", fields: { ...base.fields, [k]: f.counterexample } },
        });
      }
    }
    return failures;
  }

  if (rn.kind === "function" && ro.kind === "function") {
    const failures: Failure[] = [];
    if (rn.params.length > ro.params.length) {
      failures.push({
        path: `${path}(参数${ro.params.length + 1}·逆变)`,
        reason: `新函数需要 ${rn.params.length} 个参数，旧接口只保证提供 ${ro.params.length} 个`,
        counterexample: {
          kind: "fn-call",
          argIndex: ro.params.length,
          argument: minimalShape(rn.params[ro.params.length], env),
        },
      });
    }
    rn.params.forEach((p, i) => {
      // 参数逆变：旧参数类型 ≤ 新参数类型
      for (const f of sub(ro.params[i], p, env, `${path}(参数${i + 1}·逆变)`, visited)) {
        failures.push({
          ...f,
          counterexample: { kind: "fn-call", argIndex: i, argument: f.counterexample },
        });
      }
    });
    // 返回值协变：新返回值 ≤ 旧返回值
    for (const f of sub(rn.returns, ro.returns, env, `${path}(返回值·协变)`, visited)) {
      failures.push({ ...f, counterexample: { kind: "fn-return", returns: f.counterexample } });
    }
    return failures;
  }

  return [];
}

// ---------------------------------------------------------------------------
// 对外 API
// ---------------------------------------------------------------------------

/** 判断新接口能否替代旧接口（新 ≤ 旧） */
export function checkReplacement(newType: TypeNode, oldType: TypeNode, env: TypeEnv = {}): CheckResult {
  const failures = sub(newType, oldType, env, "$", new Set());
  return { compatible: failures.length === 0, failures };
}

export function formatReport(result: CheckResult): string {
  if (result.compatible) return "✔ 兼容：新接口可以替代旧接口";
  const lines = result.failures.map(
    (f, i) =>
      `  ${i + 1}. 路径 ${f.path}\n     原因: ${f.reason}\n     反例: ${renderShape(f.counterexample)}`,
  );
  return `✘ 不兼容：新接口不能替代旧接口（${result.failures.length} 处）\n${lines.join("\n")}`;
}
