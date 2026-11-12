import { createHash } from "node:crypto";

export const VC_BASE_CONTEXT = "https://www.w3.org/ns/credentials/v2";

export type ContextErrorCode =
  | "UNREGISTERED_CONTEXT"
  | "DIGEST_MISMATCH"
  | "MISSING_BASE_CONTEXT"
  | "INVALID_CONTEXT"
  | "PROTECTED_TERM_REDEFINITION";

export class ContextPolicyError extends Error {
  readonly code: ContextErrorCode;
  constructor(code: ContextErrorCode, message: string) {
    super(message);
    this.name = "ContextPolicyError";
    this.code = code;
  }
}

export interface RegisteredContext {
  url: string;
  digest: string;
  content: unknown;
}

function stableStringify(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(stableStringify).join(",")}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const parts = keys.map(
    (key) => `${JSON.stringify(key)}:${stableStringify(record[key])}`,
  );
  return `{${parts.join(",")}}`;
}

export function computeDigest(content: unknown): string {
  return createHash("sha256").update(stableStringify(content), "utf8").digest("hex");
}

/**
 * 调用方登记的 (URL, 内容摘要) 表。
 * 这是 context 的唯一来源；策略永远不会发起网络请求。
 */
export class ContextRegistry {
  #entries = new Map<string, { digest: string; content: unknown }>();

  register(url: string, content: unknown, digest?: string): void {
    const expected = digest ?? computeDigest(content);
    if (digest !== undefined && digest !== computeDigest(content)) {
      throw new ContextPolicyError(
        "DIGEST_MISMATCH",
        `注册时摘要不匹配: ${url}`,
      );
    }
    this.#entries.set(url, { digest: expected, content });
  }

  has(url: string): boolean {
    return this.#entries.has(url);
  }

  resolve(url: string): unknown {
    const entry = this.#entries.get(url);
    if (!entry) {
      throw new ContextPolicyError(
        "UNREGISTERED_CONTEXT",
        `拒绝加载未登记的 context（不发起网络请求）: ${url}`,
      );
    }
    if (computeDigest(entry.content) !== entry.digest) {
      throw new ContextPolicyError(
        "DIGEST_MISMATCH",
        `登记的 context 内容与摘要不符: ${url}`,
      );
    }
    return entry.content;
  }
}

export interface DocumentLoaderResult {
  contextUrl: string | null;
  document: unknown;
  documentUrl: string;
}

/**
 * 构造一个可用于 jsonld 库的 documentLoader：
 * 只查登记表，未登记即抛错，绝不触网。
 */
export function createDocumentLoader(
  registry: ContextRegistry,
): (url: string) => Promise<DocumentLoaderResult> {
  return async (url: string) => ({
    contextUrl: null,
    document: registry.resolve(url),
    documentUrl: url,
  });
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isProtectedDefinition(definition: unknown): boolean {
  if (!isPlainObject(definition)) return false;
  return definition["@protected"] === true || definition["protected"] === true;
}

/**
 * 按顺序应用一个 context 对象中的术语定义：
 * 已被保护的术语不得被重定义；新出现的 protected 术语加入保护集。
 */
function applyContextDefinitions(
  contextObject: Record<string, unknown>,
  protectedTerms: Set<string>,
  origin: string,
): void {
  for (const [term, definition] of Object.entries(contextObject)) {
    if (term.startsWith("@")) continue;
    if (protectedTerms.has(term)) {
      throw new ContextPolicyError(
        "PROTECTED_TERM_REDEFINITION",
        `受保护术语 "${term}" 不得被 ${origin} 重定义`,
      );
    }
    if (isProtectedDefinition(definition)) {
      protectedTerms.add(term);
    }
  }
}

function extractContextObject(
  document: unknown,
  origin: string,
): Record<string, unknown> {
  if (!isPlainObject(document)) {
    throw new ContextPolicyError(
      "INVALID_CONTEXT",
      `context 文档必须是对象: ${origin}`,
    );
  }
  // 远程 context 文档形如 { "@context": {...} }；内联元素本身就是 context 对象。
  const inner =
    "@context" in document && isPlainObject(document["@context"])
      ? (document["@context"] as Record<string, unknown>)
      : document;
  return inner;
}

export interface ValidateOptions {
  baseContextUrl?: string;
}

/**
 * 校验凭证的 @context：
 * 1. 必须是数组且首元素为基础凭证上下文；
 * 2. 远程 context 仅从登记表读取（未登记即拒绝，不发起网络请求）；
 * 3. 内联 context 与远程 context 接受完全相同的受保护术语检查。
 */
export function validateCredentialContexts(
  credential: unknown,
  registry: ContextRegistry,
  options: ValidateOptions = {},
): void {
  const baseContextUrl = options.baseContextUrl ?? VC_BASE_CONTEXT;
  if (!isPlainObject(credential)) {
    throw new ContextPolicyError("INVALID_CONTEXT", "凭证必须是对象");
  }
  const context = credential["@context"];
  if (!Array.isArray(context) || context.length === 0) {
    throw new ContextPolicyError(
      "MISSING_BASE_CONTEXT",
      "@context 必须是非空数组，且首元素为基础凭证上下文",
    );
  }
  if (context[0] !== baseContextUrl) {
    throw new ContextPolicyError(
      "MISSING_BASE_CONTEXT",
      `@context 首元素必须是基础凭证上下文 ${baseContextUrl}`,
    );
  }

  const protectedTerms = new Set<string>();
  for (const element of context) {
    if (typeof element === "string") {
      const document = registry.resolve(element);
      applyContextDefinitions(
        extractContextObject(document, element),
        protectedTerms,
        element,
      );
    } else if (isPlainObject(element)) {
      applyContextDefinitions(
        extractContextObject(element, "内联 context"),
        protectedTerms,
        "内联 context",
      );
    } else {
      throw new ContextPolicyError(
        "INVALID_CONTEXT",
        "@context 元素必须是 URL 字符串或内联对象",
      );
    }
  }
}
