import { classifyRelationship, parseOrigin } from "./origin.ts";
import { lookup, validateMatrix } from "./matrix.ts";
import type {
  AdmissionDecision,
  AdmissionInput,
  PolicyKey,
  PolicyMatrix,
  RedirectChainDecision,
  RedirectChainInput,
} from "./types.ts";

/**
 * 准入门：无状态地依据调用方提供的策略矩阵评估每一次资源访问。
 * 构造时即校验矩阵对全部有限组合的完备性。
 */
export class AdmissionGate {
  private readonly matrix: PolicyMatrix;

  constructor(matrix: PolicyMatrix) {
    this.matrix = matrix;
    const errors = validateMatrix(matrix);
    if (errors.length > 0) {
      throw new Error(
        `策略矩阵不完备或非法:\n${errors.join("\n")}`,
      );
    }
  }

 /** 评估单跳资源访问。 */
  evaluate(input: AdmissionInput): AdmissionDecision {
    const resourceOrigin = parseOrigin(input.resourceUrl);
    if (resourceOrigin === null) {
      return {
        verdict: "deny",
        key: {
          relationship: "cross-site",
          resourceMode: input.resourceMode,
          credentials: input.credentials,
          embedding: input.embedding,
          corp: input.declaration.corp,
          corsAccess: input.declaration.corsAccess,
        },
        reason: "invalid-origin",
      };
    }

    const relationship = classifyRelationship(
      input.documentOrigin,
      resourceOrigin,
    );
    const key: PolicyKey = {
      relationship,
      resourceMode: input.resourceMode,
      credentials: input.credentials,
      embedding: input.embedding,
      corp: input.declaration.corp,
      corsAccess: input.declaration.corsAccess,
    };

    const verdict = lookup(this.matrix, key);
    return {
      verdict,
      key,
      ...(verdict === "deny" ? { reason: "matrix-deny" as const } : {}),
    };
  }

  /**
  * 评估重定向链：每一跳都基于“文档来源 → 当前跳资源来源”
  * 重新计算来源关系并重新走矩阵判定；任一跳拒绝则整条链拒绝，
  * 后续跳不再评估。
  */
  evaluateRedirectChain(
    input: RedirectChainInput,
  ): RedirectChainDecision {
    if (input.hops.length === 0) {
      throw new Error("重定向链至少需要一跳");
    }

    const evaluations: RedirectChainDecision["evaluations"] = [];

    for (const [hopIndex, hop] of input.hops.entries()) {

      const decision = this.evaluate({
        documentOrigin: input.documentOrigin,
        resourceUrl: hop.resourceUrl,
        resourceMode: input.resourceMode,
        credentials: input.credentials,
        embedding: input.embedding,
        declaration: hop.declaration,
      });

      evaluations.push({
        hopIndex,
        resourceUrl: hop.resourceUrl,
        key: decision.key,
        verdict: decision.verdict,
      });

      if (decision.verdict === "deny") {
        return { verdict: "deny", evaluations };
      }
    }

    return { verdict: "allow", evaluations };
  }
}
