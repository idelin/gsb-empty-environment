import { evaluateCondition } from './condition.ts';
import { matchAnyGlob } from './matcher.ts';
import type {
  Policy,
  Principal,
  TrustEvaluationResult,
} from './types.ts';

export interface AssumeRoleRequest {
  trustPolicy: Policy;
  roleAccount: string;
  principal: Principal;
  resourceId: string;
  action: string;
  requestTags?: Record<string, string>;
}

export function evaluateTrust(request: AssumeRoleRequest): TrustEvaluationResult {
  const { trustPolicy, roleAccount, principal } = request;

  const allowStatement = trustPolicy.statements.find(
    (statement) =>
      statement.effect === 'Allow' &&
      (!statement.principals ||
        statement.principals.length === 0 ||
        matchAnyGlob(statement.principals, principal.id)),
  );

  if (!allowStatement) {
    return {
      decision: 'deny',
      reason: 'trust-principal-mismatch',
      roleAccount,
      principalId: principal.id,
    };
  }

  const context = {
    principal,
    action: request.action,
    resourceId: request.resourceId,
    resourceTags: {},
    requestTags: request.requestTags,
  };

  const conditionResult = evaluateCondition(allowStatement.condition, context);

  if (conditionResult.outcome === false) {
    return {
      decision: 'deny',
      reason: 'trust-account-mismatch',
      roleAccount,
      principalId: principal.id,
    };
  }

  if (conditionResult.outcome === null) {
    return {
      decision: 'deny',
      reason: 'condition-key-missing',
      roleAccount,
      principalId: principal.id,
      missingKeys: conditionResult.missingKeys,
    };
  }

  return {
    decision: 'allow',
    reason: 'allowed',
    roleAccount,
    principalId: principal.id,
  };
}
