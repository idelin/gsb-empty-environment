import { evaluateCondition } from './condition.ts';
import { matchAnyGlob, segmentMatch } from './matcher.ts';
import type {
  DenyReason,
  EvalContext,
  EvaluationResult,
  Policy,
  Statement,
  TraceEntry,
} from './types.ts';

function statementMatchesScope(statement: Statement, context: EvalContext): boolean {
  const actionMatches = matchAnyGlob(statement.actions, context.action);
  const resourceMatches = statement.resources.some((pattern) =>
    segmentMatch(pattern, context.resourceId),
  );
  return actionMatches && resourceMatches;
}

function evaluateStatement(
  statement: Statement,
  policyId: string,
  context: EvalContext,
): TraceEntry {
  const base: TraceEntry = {
    policyId,
    statementSid: statement.sid,
    effect: statement.effect,
    status: 'action-or-resource-mismatch',
  };

  if (!statementMatchesScope(statement, context)) {
    return base;
  }

  const conditionResult = evaluateCondition(statement.condition, context);
  if (conditionResult.outcome === true) {
    return { ...base, status: 'match' };
  }
  if (conditionResult.outcome === null) {
    return {
      ...base,
      status: 'condition-missing',
      missingKeys: conditionResult.missingKeys,
    };
  }
  return { ...base, status: 'condition-false' };
}

export interface PolicySetResult {
  decision: 'allow' | 'deny';
  reason: DenyReason | 'allowed';
  trace: TraceEntry[];
  matchedPolicyId?: string;
  matchedStatementSid?: string;
  deniedPolicyId?: string;
  deniedStatementSid?: string;
  missingKeys: string[];
}

export function evaluatePolicySet(
  policies: { policy: Policy; attachedAt?: string }[],
  context: EvalContext,
): PolicySetResult {
  const trace: TraceEntry[] = [];

  for (const entry of policies) {
    for (const statement of entry.policy.statements) {
      trace.push(evaluateStatement(statement, entry.policy.id, context));
    }
  }

  const explicitDeny = trace.find(
    (entry) => entry.effect === 'Deny' && entry.status === 'match',
  );
  if (explicitDeny) {
    return {
      decision: 'deny',
      reason: 'explicit-deny',
      trace,
      deniedPolicyId: explicitDeny.policyId,
      deniedStatementSid: explicitDeny.statementSid,
      missingKeys: [],
    };
  }

  const allow = trace.find(
    (entry) => entry.effect === 'Allow' && entry.status === 'match',
  );
  if (allow) {
    return {
      decision: 'allow',
      reason: 'allowed',
      trace,
      matchedPolicyId: allow.policyId,
      matchedStatementSid: allow.statementSid,
      missingKeys: [],
    };
  }

  const missingAllow = trace.find(
    (entry) => entry.effect === 'Allow' && entry.status === 'condition-missing',
  );
  if (missingAllow) {
    const missingKeys = [
      ...new Set(
        trace
          .filter((entry) => entry.status === 'condition-missing')
          .flatMap((entry) => entry.missingKeys ?? []),
      ),
    ];
    return {
      decision: 'deny',
      reason: 'condition-key-missing',
      trace,
      missingKeys,
    };
  }

  return {
    decision: 'deny',
    reason: 'no-matching-statement',
    trace,
    missingKeys: [],
  };
}

export function toEvaluationResult(result: PolicySetResult): EvaluationResult {
  return {
    decision: result.decision,
    reason: result.reason,
    matchedPolicyId: result.matchedPolicyId,
    matchedStatementSid: result.matchedStatementSid,
    deniedPolicyId: result.deniedPolicyId,
    deniedStatementSid: result.deniedStatementSid,
    missingKeys: result.missingKeys.length ? result.missingKeys : undefined,
    trace: result.trace,
  };
}
