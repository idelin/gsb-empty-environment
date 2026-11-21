import { ResourceTree } from './hierarchy.ts';
import { evaluatePolicySet, toEvaluationResult } from './policy.ts';
import type {
  AccessRequest,
  EvalContext,
  EvaluationResult,
} from './types.ts';

export class PermissionEngine {
  private readonly tree: ResourceTree;

  constructor(tree: ResourceTree) {
    this.tree = tree;
  }

  evaluate(request: AccessRequest): EvaluationResult {
    const resourceTags = this.tree.effectiveTags(request.resourceId);

    const context: EvalContext = {
      principal: request.principal,
      action: request.action,
      resourceId: request.resourceId,
      resourceTags,
      requestTags: request.requestTags,
      extra: request.extra,
    };

    const identityPolicies = (request.identityPolicies ?? []).map((policy) => ({
      policy,
    }));
    const resourcePolicies = this.tree.applicablePolicies(request.resourceId);

    const identityResult = evaluatePolicySet(identityPolicies, context);
    const resourceResult =
      resourcePolicies.length === 0
        ? undefined
        : evaluatePolicySet(resourcePolicies, context);

    if (
      identityResult.decision === 'deny' &&
      identityResult.reason === 'explicit-deny'
    ) {
      return toEvaluationResult(identityResult);
    }
    if (
      resourceResult?.decision === 'deny' &&
      resourceResult.reason === 'explicit-deny'
    ) {
      return toEvaluationResult(resourceResult);
    }

    const identityAllowed = identityResult.decision === 'allow';
    const resourceAllowed = resourceResult === undefined || resourceResult.decision === 'allow';

    if (identityAllowed && resourceAllowed) {
      return toEvaluationResult(identityResult);
    }

    if (!identityAllowed && !resourceAllowed) {
      if (resourceResult === undefined) {
        return toEvaluationResult(identityResult);
      }
      const chosen =
        identityResult.reason === 'condition-key-missing'
          ? identityResult
          : resourceResult.reason === 'condition-key-missing'
            ? resourceResult
            : identityResult;
      return toEvaluationResult(chosen);
    }

    const failing = identityAllowed ? (resourceResult ?? identityResult) : identityResult;
    return toEvaluationResult(failing);
  }
}
