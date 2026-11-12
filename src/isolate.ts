import { evaluateKeyAccess } from './keys.ts';
import {
  evaluateProtectedOverwrite,
  evaluateTargetOwnership,
} from './ownership.ts';
import { planRefresh } from './refresh.ts';
import { evaluateStoreAccess } from './storeAccess.ts';
import {
  DENIAL_CODES,
  type Finding,
  type IsolationDecision,
  type IsolationEvaluation,
} from './types.ts';

/**
 * 每次调谐调用的隔离判定总入口，判定顺序：
 * 1. 存储引用与命名空间隔离；
 * 2. key 前缀与 find.name 正则越界；
 * 3. 目标 Secret 属主（Owner）；
 * 4. Merge 保护标签覆盖；
 * 5. 刷新失败与空值降级。
 *
 * 任一拒绝性违例返回 deny；刷新类问题返回 skip；全部通过返回 apply。
 */
export function evaluateIsolation(
  evaluation: IsolationEvaluation,
): IsolationDecision {
  const { externalSecret, store, namespaceLabels, existingTarget, self, providerKeys, refresh } =
    evaluation;
  const findings: Finding[] = [];

  const storeFindings = evaluateStoreAccess(
    externalSecret,
    store,
    namespaceLabels,
  );
  findings.push(...storeFindings);

  if (store && !storeFindings.some((finding) => DENIAL_CODES.has(finding.code))) {
    findings.push(
      ...evaluateKeyAccess(externalSecret.dataFrom, store, providerKeys),
    );
  }

  if (externalSecret.creationPolicy === 'Owner') {
    findings.push(...evaluateTargetOwnership(existingTarget, self));
  }

  const incomingKeys = Object.keys(refresh.fetched ?? {});
  if (externalSecret.creationPolicy === 'Merge') {
    findings.push(
      ...evaluateProtectedOverwrite(existingTarget, incomingKeys),
    );
  }

  const denials = findings.filter((finding) => DENIAL_CODES.has(finding.code));
  if (denials.length > 0) {
    return {
      action: 'deny',
      allowed: false,
      denials,
      notices: findings.filter((finding) => !DENIAL_CODES.has(finding.code)),
      plannedData: {},
    };
  }

  if (
    externalSecret.creationPolicy === 'None' &&
    !existingTarget
  ) {
    return {
      action: 'skip',
      allowed: true,
      denials: [],
      notices: [
        {
          code: 'TARGET_MISSING_WITH_NONE_POLICY',
          message: `creationPolicy 为 None 且目标 Secret ${externalSecret.targetSecretName} 不存在，跳过创建`,
        },
      ],
      plannedData: {},
    };
  }

  const plan = planRefresh(refresh, existingTarget);
  if (plan.skip) {
    return {
      action: 'skip',
      allowed: true,
      denials: [],
      notices: [...findings, ...plan.findings],
      plannedData: {},
    };
  }

  return {
    action: 'apply',
    allowed: true,
    denials: [],
    notices: [...findings, ...plan.findings],
    plannedData: plan.plannedData,
  };
}
