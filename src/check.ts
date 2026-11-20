/**
 * 变更检查入口：判断热切换是否会对旧策略允许的已建立连接造成权限扩大。
 *
 * 方法（全枚举，非抽样）：
 *  1. 由两份策略的前缀/端口条件边界切出全部抽象类别；
 *  2. 每个类别中从 (0, 0) 做标签对可达性搜索（切换前旧策略可处理
 *     任意前缀的报文，切换后新旧策略从同一旧标签并行继续）；
 *  3. 对每一对可达标签核对授权包含关系：旧策略（旧标签）拒绝而
 *     新策略（新标签）允许（任一方向），即为权限扩大。
 *
 * 报告仅包含：通过/拒绝结论与违反的规则编号；通过时附带可独立
 * 验证的证书（verifyCertificate）。
 */

import {
  CERTIFICATE_VERSION,
  classToCert,
  findViolations,
  pairToCert,
} from "./certificate.ts";
import type { CertViolation, Certificate } from "./certificate.ts";
import { computeClasses } from "./classes.ts";
import { compilePolicy } from "./model.ts";
import type { Policy } from "./model.ts";
import { reachablePairs } from "./reachability.ts";

export interface CheckStats {
  /** 抽象类别（地址/端口等价类）总数。 */
  classes: number;
  /** 可达标签对总数（所有类别合计）。 */
  pairs: number;
}

export interface CheckResult {
  /** pass = 不存在权限扩大；fail = 发现权限扩大。 */
  verdict: "pass" | "fail";
  /** 违反包含关系的（类别, 标签对, 规则编号）列表。 */
  violations: CertViolation[];
  stats: CheckStats;
  /** 不扩大证明；可 JSON 序列化后用 verifyCertificate 独立验证。 */
  certificate: Certificate;
}

export function checkChange(oldPolicy: Policy, newPolicy: Policy): CheckResult {
  const oldCompiled = compilePolicy(oldPolicy, "oldPolicy");
  const newCompiled = compilePolicy(newPolicy, "newPolicy");
  const classes = computeClasses(oldPolicy, newPolicy);

  const violations: CertViolation[] = [];
  const certPairs: Certificate["pairs"] = [];
  let pairCount = 0;

  for (const cls of classes) {
    for (const pair of reachablePairs(oldCompiled, newCompiled, cls)) {
      pairCount += 1;
      certPairs.push(pairToCert(cls.id, pair));
      violations.push(
        ...findViolations(oldCompiled, newCompiled, cls, pair.oldLabel, pair.newLabel),
      );
    }
  }

  const certificate: Certificate = {
    version: CERTIFICATE_VERSION,
    classes: classes.map(classToCert),
    pairs: certPairs,
    violations,
  };

  return {
    verdict: violations.length === 0 ? "pass" : "fail",
    violations,
    stats: { classes: classes.length, pairs: pairCount },
    certificate,
  };
}
