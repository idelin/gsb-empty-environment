import type {
  CredentialsMode,
  EmbeddingPolicy,
  PolicyKey,
  PolicyMatrix,
  ResourceDeclaration,
  ResourceMode,
  OriginRelationship,
  Verdict,
} from "./types.ts";

/** 全部有限属性域。 */
export const RELATIONSHIPS: readonly OriginRelationship[] = [
  "same-origin",
  "same-site",
  "cross-site",
];

export const RESOURCE_MODES: readonly ResourceMode[] = [
  "navigate",
  "cors",
  "no-cors",
];

export const CREDENTIALS_MODES: readonly CredentialsMode[] = [
  "include",
  "same-origin",
  "omit",
];

export const EMBEDDING_POLICIES: readonly EmbeddingPolicy[] = [
  "unsafe-none",
  "require-corp",
  "credentialless",
];

export const CORP_VALUES: readonly ResourceDeclaration["corp"][] = [
  "none",
  "same-origin",
  "same-site",
  "cross-origin",
];

export const CORS_ACCESS_VALUES: readonly boolean[] = [false, true];

/**
 * 策略键的规范化序列化。
 * 字段顺序固定，保证同一组合永远映射到同一字符串。
 */
export function serializeKey(key: PolicyKey): string {
  return [
    key.relationship,
    key.resourceMode,
    key.credentials,
    key.embedding,
    key.corp,
    String(key.corsAccess),
  ].join("|");
}

export function deserializeKey(serialized: string): PolicyKey {
  const parts = serialized.split("|");
  if (parts.length !== 6) {
    throw new Error(`非法策略键: ${serialized}`);
  }
  const [relationship, resourceMode, credentials, embedding, corp, cors] =
    parts as [string, string, string, string, string, string];
  return {
    relationship: relationship as PolicyKey["relationship"],
    resourceMode: resourceMode as PolicyKey["resourceMode"],
    credentials: credentials as PolicyKey["credentials"],
    embedding: embedding as PolicyKey["embedding"],
    corp: corp as PolicyKey["corp"],
    corsAccess: cors === "true",
  };
}

/** 枚举策略空间中的全部有限组合（3×3×3×3×4×2 = 648 个键）。 */
export function enumerateKeys(): PolicyKey[] {
  const keys: PolicyKey[] = [];
  for (const relationship of RELATIONSHIPS) {
    for (const resourceMode of RESOURCE_MODES) {
      for (const credentials of CREDENTIALS_MODES) {
        for (const embedding of EMBEDDING_POLICIES) {
          for (const corp of CORP_VALUES) {
            for (const corsAccess of CORS_ACCESS_VALUES) {
              keys.push({
                relationship,
                resourceMode,
                credentials,
                embedding,
                corp,
                corsAccess,
              });
            }
          }
        }
      }
    }
  }
  return keys;
}

interface MatrixRule {
  match: (key: PolicyKey) => boolean;
  verdict: Verdict;
}

function corpPermits(
  corp: ResourceDeclaration["corp"],
  relationship: OriginRelationship,
): boolean {
  if (corp === "cross-origin") {
    return true;
  }
  if (corp === "same-site") {
    return relationship !== "cross-site";
  }
  if (corp === "same-origin") {
    return relationship === "same-origin";
  }
  return false;
}

/**
 * 默认矩阵规则（按优先级匹配，模拟 COEP + CORP + CORS 的 Web 平台语义）：
 *
 * 1. 同源响应始终可嵌入（CORP 是跨源防护机制，不约束同源）。
 * 2. 顶层导航不受 COEP 嵌入要求约束。
 * 3. CORS 模式：CORS 校验通过即获许（corsAccess=true）。
 * 4. COEP: unsafe-none（未启用隔离）：跨源 no-cors 响应允许。
  * 5. COEP: require-corp（跨源隔离）：跨源响应必须由 CORP 或 CORS 显式获许。
  * 6. COEP: credentialless（无凭据隔离）：
   *    跨源响应必须由 CORP 或 CORS 显式获许；凭据模式仍作为独立维度
   *    保留在矩阵中，调用方可据此施加更严格的自定义门控。
 * 7. 兜底拒绝：未被任何规则显式获许的组合一律拒绝。
 */
const DEFAULT_RULES: readonly MatrixRule[] = [
  {
    match: (key) => key.relationship === "same-origin",
    verdict: "allow",
  },
  {
    match: (key) => key.resourceMode === "navigate",
    verdict: "allow",
  },
  {
    match: (key) => key.resourceMode === "cors" && key.corsAccess,
    verdict: "allow",
  },
  {
    match: (key) =>
      key.resourceMode === "no-cors" && key.embedding === "unsafe-none",
    verdict: "allow",
  },
  {
    match: (key) =>
      key.resourceMode === "no-cors" &&
      key.embedding === "require-corp" &&
      corpPermits(key.corp, key.relationship),
    verdict: "allow",
  },
  {
    match: (key) =>
      key.resourceMode === "no-cors" &&
      key.embedding === "credentialless" &&
      (corpPermits(key.corp, key.relationship) || key.corsAccess),
    verdict: "allow",
  },
  {
    match: () => true,
    verdict: "deny",
  },
];

/** 构造符合默认 Web 平台语义、覆盖全部有限组合的策略矩阵。 */
export function createDefaultMatrix(): PolicyMatrix {
  const matrix: Record<string, Verdict> = {};
  for (const key of enumerateKeys()) {
    const rule = DEFAULT_RULES.find((candidate) => candidate.match(key));
    matrix[serializeKey(key)] = rule ? rule.verdict : "deny";
  }
  return matrix;
}

/**
 * 校验调用方矩阵：
 * - 必须为有限属性空间中的每个组合给出 allow/deny；
 * - 不允许出现未知键。
 * 返回的错误列表为空即矩阵完备且合法。
 */
export function validateMatrix(matrix: PolicyMatrix): string[] {
  const errors: string[] = [];
  const expected = new Set(enumerateKeys().map(serializeKey));

  for (const key of expected) {
    if (!(key in matrix)) {
      errors.push(`缺少策略组合: ${key}`);
    } else if (matrix[key] !== "allow" && matrix[key] !== "deny") {
      errors.push(`非法判定值（仅允许 allow/deny）: ${key}=${matrix[key]}`);
    }
  }
  for (const key of Object.keys(matrix)) {
    if (!expected.has(key)) {
      errors.push(`未知策略组合: ${key}`);
    }
  }
  return errors;
}

/** 在矩阵中查找单个有限组合的判定；矩阵非法时抛出错误。 */
export function lookup(matrix: PolicyMatrix, key: PolicyKey): Verdict {
  const serialized = serializeKey(key);
  const verdict = matrix[serialized];
  if (verdict !== "allow" && verdict !== "deny") {
    throw new Error(
      `策略矩阵缺少组合 ${serialized}，请先通过 validateMatrix 校验`,
    );
  }
  return verdict;
}
