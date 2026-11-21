/**
 * 结果引用解析（RFC 8620 §3.6 Result References、§3.7 Result References
 * for Objects Within Arrays）。
 *
 * 引用合法性要求：
 * 1. resultOf 指向同请求内更早的调用；
 * 2. 被引用调用必须成功（错误响应不可被引用）；
 * 3. name 与被引用调用的方法名一致；
 * 4. path 是可解析的 JSON 指针（支持 RFC 6901 转义与 "*" 通配）。
 */

import { ResultReferenceError } from "./types.ts";
import type {
  CompletedCall,
  ResultReference,
} from "./types.ts";

const RESULT_REFERENCE_KEY = "#result";

/** 判断一个值是否为结果引用对象。 */
export function isResultReference(
  value: unknown,
): value is ResultReference {
  return (
    typeof value === "object" &&
    value !== null &&
    !Array.isArray(value) &&
    typeof (value as Record<string, unknown>).resultOf === "string" &&
    typeof (value as Record<string, unknown>).name === "string" &&
    typeof (value as Record<string, unknown>).path === "string"
  );
}

/** RFC 6901 token 反转义：~1 -> /，~0 -> ~。 */
function unescapeToken(token: string): string {
  return token.replace(/~1/g, "/").replace(/~0/g, "~");
}

/**
 * 沿 JSON 指针取值。
 * 末尾 token 为 "*" 时对数组逐项求值并拍平一层（RFC 8620 §3.7）。
 */
function resolvePointer(
  root: unknown,
  path: string,
): unknown {
  if (path === "") {
    return root;
  }
  if (!path.startsWith("/")) {
    throw new ResultReferenceError(
      `结果引用路径 ${path} 不是合法的 JSON 指针（必须以 / 开头）`,
      { path },
    );
  }

  const tokens = path.slice(1).split("/").map(unescapeToken);
  let current = root;

  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    const last = index === tokens.length - 1;

    if (token === "*" && last) {
      if (!Array.isArray(current)) {
        throw new ResultReferenceError(
          `结果引用路径 ${path} 中的 "*" 通配要求当前值为数组`,
          { path },
        );
      }
      return current;
    }

    if (Array.isArray(current)) {
      const position = Number(token);
      if (!Number.isInteger(position) || position < 0 ||
        position >= current.length) {
        throw new ResultReferenceError(
          `结果引用路径 ${path} 的数组下标 ${token} 越界`,
          { path, token },
        );
      }
      current = current[position];
    } else if (
      typeof current === "object" &&
      current !== null &&
      Object.prototype.hasOwnProperty.call(current, token)
    ) {
      current = (current as Record<string, unknown>)[token];
    } else {
      throw new ResultReferenceError(
        `结果引用路径 ${path} 在 token "${token}" 处无法解析`,
        { path, token },
      );
    }
  }

  return current;
}

/** 解析单个顶层结果引用，并返回引用数据的来源账户。 */
export function resolveReference(
  reference: ResultReference,
  completed: ReadonlyMap<string, CompletedCall>,
): { value: unknown; originAccountId: string } {
  const target = completed.get(reference.resultOf);

  // 同请求内更早的调用才会出现在 completed 中；不存在即前向引用或未知 id。
  if (target === undefined) {
    throw new ResultReferenceError(
      `结果引用指向不存在或尚未执行的调用 ${reference.resultOf}`,
      { resultOf: reference.resultOf },
    );
  }
  if (!target.succeeded) {
    throw new ResultReferenceError(
      `结果引用指向的调用 ${reference.resultOf} 未成功，错误响应不可被引用`,
      { resultOf: reference.resultOf },
    );
  }
  if (target.methodName !== reference.name) {
    throw new ResultReferenceError(
      `结果引用的方法名 ${reference.name} 与调用 ${reference.resultOf} 的实际方法名 ${target.methodName} 不一致`,
      { resultOf: reference.resultOf, expected: reference.name,
        actual: target.methodName },
    );
  }

  return {
    value: resolvePointer(target.responseArgs, reference.path),
    originAccountId: target.accountId,
  };
}

/** 参数中经引用解析后得到的值，携带其来源账户以便重新授权。 */
export interface ReferencedValue {
  value: unknown;
  originAccountId: string;
}

/**
 * 递归解析参数中的所有结果引用（含 §3.7 嵌套引用）。
 * 返回替换后的参数，以及每个引用解出值的来源账户集合。
 */
export function resolveReferences(
  args: Record<string, unknown>,
  completed: ReadonlyMap<string, CompletedCall>,
): {
  resolvedArgs: Record<string, unknown>;
  referencedAccounts: ReadonlySet<string>;
} {
  const referencedAccounts = new Set<string>();

  const walk = (node: unknown): unknown => {
    if (isResultReference(node)) {
      const { value, originAccountId } = resolveReference(node, completed);
      referencedAccounts.add(originAccountId);
      // 引用解出的值内部不再包含引用，按原值返回。
      return value;
    }
    if (Array.isArray(node)) {
      return node.map(walk);
    }
    if (typeof node === "object" && node !== null) {
      const result: Record<string, unknown> = {};
      for (const [key, child] of Object.entries(node)) {
        result[key] = walk(child);
      }
      return result;
    }
    return node;
  };

  const resolvedArgs = walk(args) as Record<string, unknown>;
  return { resolvedArgs, referencedAccounts };
}

export { RESULT_REFERENCE_KEY };
