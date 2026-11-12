import { AdmissionGate } from "./admission.ts";
import { SecurityContextCache, buildCacheKey } from "./cache.ts";
import type {
  AdmissionInput,
  CacheEntry,
  PolicyMatrix,
  RedirectChainInput,
} from "./types.ts";

export interface AdmitResult {
  allowed: boolean;
  /** 命中既有缓存时为 true（缓存条目与当前完整安全上下文一致）。 */
  fromCache: boolean;
  cacheKey: string;
}

/**
 * 浏览器宿主门面：把准入门与安全上下文缓存组合成一次调用。
 *
 * 调用流程（每一跳都独立适用）：
 * 1. 依据结构化来源派生来源关系，连同其余有限属性组成策略键；
 * 2. 在调用方提供的矩阵中查找 allow/deny；
 * 3. allow 才写入/命中绑定完整安全上下文的缓存；deny 不产生缓存。
 */
export class HostAdmission {
  private readonly gate: AdmissionGate;
  private readonly cache: SecurityContextCache;

  constructor(matrix: PolicyMatrix) {
    this.gate = new AdmissionGate(matrix);
    this.cache = new SecurityContextCache();
  }

  /** 评估一次资源访问并在获许时写入缓存。 */
  admit(input: AdmissionInput): AdmitResult {
    const decision = this.gate.evaluate(input);
    const cacheKey = buildCacheKey(
      input.resourceUrl,
      input.documentOrigin,
      decision.key,
    );

    if (decision.verdict === "deny") {
      return { allowed: false, fromCache: false, cacheKey };
    }

    const existing = this.cache.get(
      input.resourceUrl,
      input.documentOrigin,
      decision.key,
    );
    const entry: CacheEntry = {
      resourceUrl: input.resourceUrl,
      documentOrigin: input.documentOrigin,
      resourceMode: input.resourceMode,
      credentials: input.credentials,
      embedding: input.embedding,
      relationship: decision.key.relationship,
      corp: input.declaration.corp,
      corsAccess: input.declaration.corsAccess,
      verdict: "allow",
    };
    this.cache.put(entry);

    return {
      allowed: true,
      fromCache: existing !== undefined,
      cacheKey,
    };
  }

  /**
   * 评估重定向链：逐跳重新计算并准入，全部获许后只把末跳
   * （最终资源 + 最终安全上下文）写入缓存。
   */
  admitRedirectChain(
    input: RedirectChainInput,
  ): { allowed: boolean; evaluated: ReturnType<AdmissionGate["evaluateRedirectChain"]> } {
    const evaluated = this.gate.evaluateRedirectChain(input);
    if (evaluated.verdict === "deny") {
      return { allowed: false, evaluated };
    }
    const lastHop = input.hops.at(-1);
    const lastEval = evaluated.evaluations.at(-1);
    if (lastHop === undefined || lastEval === undefined) {
      throw new Error("重定向链至少需要一跳");
    }
    this.cache.put({
      resourceUrl: lastHop.resourceUrl,
      documentOrigin: input.documentOrigin,
      resourceMode: input.resourceMode,
      credentials: input.credentials,
      embedding: input.embedding,
      relationship: lastEval.key.relationship,
      corp: lastHop.declaration.corp,
      corsAccess: lastHop.declaration.corsAccess,
      verdict: "allow",
    });
    return { allowed: true, evaluated };
  }

  /** 直接读取缓存（用于宿主验证某上下文是否已有获许响应）。 */
  lookupCache(input: AdmissionInput): CacheEntry | undefined {
    const decision = this.gate.evaluate(input);
    return this.cache.get(
      input.resourceUrl,
      input.documentOrigin,
      decision.key,
    );
  }

  get cacheSize(): number {
    return this.cache.size;
  }
}
