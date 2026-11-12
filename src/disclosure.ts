import { FedcmError } from "./errors.ts";

/** 断言（token）载荷，`email` 是唯一受披露要求约束的字段。 */
export interface AssertionClaims {
  sub: string;
  email?: string;
  [key: string]: unknown;
}

/**
 * 硬性策略：只有浏览器确认展示了披露文本（`disclosure_text_shown === true`）
 * 才允许签发含邮箱的断言。缺失或为 false 时，包含邮箱一律拒绝。
 */
export function assertEmailDisclosure(
  claims: AssertionClaims,
  disclosureTextShown: boolean,
): asserts claims is AssertionClaims & { email: string } {
  if (claims.email !== undefined && disclosureTextShown !== true) {
    throw new FedcmError({
      reason: "disclosure_required",
      status: 403,
      message:
        "An assertion containing an email may only be issued when " +
        "disclosure_text_shown is true.",
    });
  }
}

/**
 * 安全构造断言载荷：当披露文本未展示时自动剔除 `email` 字段，
 * 适用于“邮箱可选、未披露则降级为无邮箱断言”的 IdP。
 * 与 {@link assertEmailDisclosure} 二选一使用。
 */
export function withEmailClaim(
  base: Omit<AssertionClaims, "email">,
  email: string | undefined,
  disclosureTextShown: boolean,
): AssertionClaims {
  const claims: AssertionClaims = { ...base };
  if (email !== undefined && disclosureTextShown === true) {
    claims.email = email;
  }
  return claims;
}
