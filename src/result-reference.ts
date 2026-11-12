// RFC 8620 §3.7 结果引用（ResultReference）解析。

import { JsonPointerError, resolvePointer } from "./json-pointer.ts";
import type { ClientId, MethodName, MethodResponse } from "./types.ts";

/** 结果引用对象的形态：{"resultOf": ..., "name": ..., "path": ...}。 */
export interface ResultReference {
  resultOf: ClientId;
  name: MethodName;
  path: string;
}

/** 一次替换记录：引用位置、解出的值以及绑定信息（供二次授权）。 */
export interface ReferenceReplacement {
  reference: ResultReference;
  value: unknown;
  /** 引用所指向的前置调用的方法名（即前置调用本身，非 name 匹配值）。 */
  sourceMethodName: MethodName;
}

export type Resolution =
  | { ok: true; resolved: unknown; replacements: ReferenceReplacement[] }
  | { ok: false; detail: string };

export function isResultReference(value: unknown): value is ResultReference {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate.resultOf === "string" &&
    typeof candidate.name === "string" &&
    typeof candidate.path === "string"
  );
}

interface WalkContext {
  // 已记录的方法响应（仅成功调用会写入），key 为客户端 id。
  responses: Map<ClientId, MethodResponse>;
  replacements: ReferenceReplacement[];
  error?: string;
}

function walk(value: unknown, ctx: WalkContext): unknown {
  if (ctx.error !== undefined) {
    return value;
  }
  if (isResultReference(value)) {
    const reference = value;
    const prior = ctx.responses.get(reference.resultOf);
    // 引用必须指向同请求内更早且成功的调用：未登记即不存在或已失败。
    if (prior === undefined) {
      ctx.error =
        `结果引用指向的调用 ${reference.resultOf} 不存在、尚未执行或未成功`;
      return value;
    }
    const [priorMethodName, priorArgs] = prior;
    if (priorMethodName !== reference.name) {
      ctx.error =
        `结果引用的 name "${reference.name}" 与调用 ` +
        `${reference.resultOf} 的实际方法名 "${priorMethodName}" 不一致`;
      return value;
    }
    let referenced: unknown;
    try {
      referenced = resolvePointer(priorArgs, reference.path);
    } catch (error) {
      const reason =
        error instanceof JsonPointerError ? error.message : String(error);
      ctx.error = `结果引用路径无法解析: ${reason}`;
      return value;
    }
    ctx.replacements.push({
      reference,
      value: referenced,
      sourceMethodName: priorMethodName,
    });
    return referenced;
  }
  if (Array.isArray(value)) {
    return value.map((item) => walk(item, ctx));
  }
  if (value !== null && typeof value === "object") {
    const source = value as Record<string, unknown>;
    const clone: Record<string, unknown> = {};
    for (const key of Object.keys(source)) {
      clone[key] = walk(source[key], ctx);
    }
    return clone;
  }
  return value;
}

/**
 * 深拷贝 args 并把其中所有结果引用替换为前置成功调用的输出值。
 * 任何引用非法都返回失败，由调用方产生 invalidResultReference。
 */
export function resolveReferences(
  args: Record<string, unknown>,
  responses: Map<ClientId, MethodResponse>,
): Resolution {
  const ctx: WalkContext = { responses, replacements: [] };
  const resolved = walk(args, ctx) as Record<string, unknown>;
  if (ctx.error !== undefined) {
    return { ok: false, detail: ctx.error };
  }
  return { ok: true, resolved, replacements: ctx.replacements };
}
