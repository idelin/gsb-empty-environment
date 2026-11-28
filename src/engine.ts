import { CelError, CelValue, compile, evaluate } from './cel/index.ts';
import { matchResources } from './matching.ts';
import { ParameterStore, ParameterKey } from './parameters.ts';
import {
  AdmissionDecision,
  AdmissionRequest,
  BindingEvaluation,
  FailurePolicy,
  JsonValue,
  ParameterObject,
  ValidatingAdmissionPolicy,
  ValidatingAdmissionPolicyBinding,
  ValidationAction,
  ValidationFailure,
} from './types.ts';

interface CompiledVariable {
  name: string;
  expression: string;
}

interface CompiledValidation {
  expression: string;
  message?: string;
  messageExpression?: string;
}

interface CompiledPolicy {
  name: string;
  raw: ValidatingAdmissionPolicy;
  failurePolicy: FailurePolicy;
  variables: CompiledVariable[];
  matchConditions: Array<{ name: string; expression: string }>;
  validations: CompiledValidation[];
}

interface FailureEntry {
  binding: string;
  policy: string;
  validationIndex: number;
  message: string;
  actions: ValidationAction[];
  parameterKey?: ParameterKey;
}

function toCelValue(value: JsonValue | undefined): CelValue {
  return value as CelValue;
}

/**
 * Declarative admission evaluation engine.
 *
 * Policies and bindings are registered up front; each admission request is
 * evaluated against every binding whose policyName resolves to a registered
 * policy. All matching bindings contribute their Deny/Warn/Audit actions even
 * after another binding has already denied the request.
 */
export class ValidatingAdmissionEngine {
  private readonly policies = new Map<string, CompiledPolicy>();
  private readonly bindings: ValidatingAdmissionPolicyBinding[] = [];
  readonly parameterStore = new ParameterStore();

  registerPolicy(policy: ValidatingAdmissionPolicy): void {
    const name = policy.metadata?.name;
    if (!name) throw new Error('ValidatingAdmissionPolicy requires metadata.name');
    const spec = policy.spec;

    const compiled: CompiledPolicy = {
      name,
      raw: policy,
      failurePolicy: spec.failurePolicy ?? 'Fail',
      variables: (spec.variables ?? []).map((v) => ({
        name: v.name,
        expression: v.expression,
      })),
      matchConditions: (spec.matchConditions ?? []).map((c) => ({
        name: c.name,
        expression: c.expression,
      })),
      validations: (spec.validations ?? []).map((v) => ({
        expression: v.expression,
        message: v.message,
        messageExpression: v.messageExpression,
      })),
    };

    // Eagerly compile every expression so syntax errors surface at registration.
    for (const variable of compiled.variables) compile(variable.expression);
    for (const condition of compiled.matchConditions) compile(condition.expression);
    for (const validation of compiled.validations) {
      compile(validation.expression);
      if (validation.messageExpression) compile(validation.messageExpression);
    }

    this.policies.set(name, compiled);
  }

  registerBinding(binding: ValidatingAdmissionPolicyBinding): void {
    if (!binding.metadata?.name) {
      throw new Error('ValidatingAdmissionPolicyBinding requires metadata.name');
    }
    if (!binding.spec.policyName) {
      throw new Error('ValidatingAdmissionPolicyBinding requires spec.policyName');
    }
    const actions = binding.spec.validationActions ?? ['Deny'];
    for (const action of actions) {
      if (!['Deny', 'Warn', 'Audit'].includes(action)) {
        throw new Error(`unknown validationAction ${action}`);
      }
    }
    this.bindings.push(binding);
  }

  addParameter(object: ParameterObject): void {
    this.parameterStore.upsert(object);
  }

  removeParameter(namespace: string, name: string): void {
    this.parameterStore.remove(namespace, name);
  }

  evaluate(request: AdmissionRequest): AdmissionDecision {
    const bindingEvaluations: BindingEvaluation[] = [];
    const allFailures: FailureEntry[] = [];

    for (const binding of this.bindings) {
      const result = this.evaluateBinding(binding, request);
      bindingEvaluations.push(result.evaluation);
      allFailures.push(...result.failures);
    }

    const failures: ValidationFailure[] = allFailures.map((entry) => ({
      binding: entry.binding,
      policy: entry.policy,
      validationIndex: entry.validationIndex,
      message: entry.message,
      actions: entry.actions,
      parameter: entry.parameterKey
        ? `${entry.parameterKey.namespace}/${entry.parameterKey.name}`
        : undefined,
    }));

    const denyFailures = allFailures.filter((entry) => entry.actions.includes('Deny'));
    const denied = denyFailures.length > 0;

    const warnings: string[] = [];
    const auditAnnotations: Record<string, string> = {};
    for (const entry of allFailures) {
      if (entry.actions.includes('Warn')) {
        warnings.push(`${entry.binding}: ${entry.message}`);
      }
      if (entry.actions.includes('Audit')) {
        const suffix = entry.parameterKey
          ? `:${entry.parameterKey.namespace}_${entry.parameterKey.name}`
          : '';
        const key = `validation.admission.k8s.io/${entry.binding}${suffix}:${entry.validationIndex}`;
        auditAnnotations[key] = entry.message;
      }
    }

    return {
      allowed: !denied,
      denied,
      warnings,
      auditAnnotations,
      failures,
      bindingEvaluations,
      reasons: denyFailures.map((entry) => `${entry.binding}: ${entry.message}`),
    };
  }

  private evaluateBinding(
    binding: ValidatingAdmissionPolicyBinding,
    request: AdmissionRequest,
  ): { evaluation: BindingEvaluation; failures: FailureEntry[] } {
    const bindingName = binding.metadata!.name!;
    const spec = binding.spec;
    const baseEvaluation: BindingEvaluation = {
      binding: bindingName,
      policy: spec.policyName,
      matched: false,
      parameterKeys: [],
      failures: [],
      validationActions: spec.validationActions ?? ['Deny'],
      errored: false,
    };

    const policy = this.policies.get(spec.policyName);
    if (!policy) {
      return {
        evaluation: {
          ...baseEvaluation,
          skippedReason: `policy '${spec.policyName}' not found`,
        },
        failures: [],
      };
    }

    // 1. Resource/selector matching: policy constraints AND binding constraints must pass.
    if (!matchResources(policy.raw.spec.matchConstraints, request)) {
      return {
        evaluation: {
          ...baseEvaluation,
          skippedReason: 'policy matchConstraints did not match',
        },
        failures: [],
      };
    }
    if (!matchResources(spec.matchResources, request)) {
      return {
        evaluation: {
          ...baseEvaluation,
          skippedReason: 'binding matchResources did not match',
        },
        failures: [],
      };
    }

    // 2. Parameter resolution.
    const resolution = this.parameterStore.resolve(
      spec.paramRef,
      policy.raw.spec.paramKind,
      request.namespace,
    );
    const notFoundAction = spec.paramRef?.parameterNotFoundAction ?? 'Deny';
    if (!resolution.found) {
      if (notFoundAction === 'Allow') {
        return {
          evaluation: {
            ...baseEvaluation,
            matched: true,
            skippedReason:
              'parameter not found; parameterNotFoundAction=Allow',
          },
          failures: [],
        };
      }
      const message = `parameter not found for binding '${bindingName}'; parameterNotFoundAction=Deny`;
      const failure: FailureEntry = {
        binding: bindingName,
        policy: policy.name,
        validationIndex: -1,
        message,
        actions: baseEvaluation.validationActions,
      };
      return {
        evaluation: {
          ...baseEvaluation,
          matched: true,
          errored: true,
          errorMessage: message,
        },
        failures: [failure],
      };
    }

    // A policy without paramKind (binding without paramRef) evaluates exactly once.
    const parameterInstances =
      resolution.objects.length === 0
        ? [
            {
              key: undefined as ParameterKey | undefined,
              object: undefined as ParameterObject | undefined,
            },
          ]
        : resolution.objects;

    const failures: FailureEntry[] = [];
    const parameterKeys: string[] = [];

    // 3. Evaluate independently once per resolved parameter object.
    let errored = false;
    let errorMessage: string | undefined;
    for (const instance of parameterInstances) {
      if (instance.key) {
        parameterKeys.push(`${instance.key.namespace}/${instance.key.name}`);
      }
      const outcome = this.evaluateOnce(
        policy,
        binding,
        request,
        instance.object,
        instance.key,
      );
      if (outcome.skipped) continue;
      failures.push(...outcome.failures);
      for (const failure of outcome.failures) {
        if (failure.validationIndex === -1) {
          errored = true;
          errorMessage = failure.message;
        }
      }
    }

    return {
      evaluation: {
        ...baseEvaluation,
        matched: true,
        parameterKeys,
        errored,
        errorMessage,
        failures: failures.map((entry) => ({
          binding: entry.binding,
          policy: entry.policy,
          validationIndex: entry.validationIndex,
          message: entry.message,
          actions: entry.actions,
          parameter: entry.parameterKey
            ? `${entry.parameterKey.namespace}/${entry.parameterKey.name}`
            : undefined,
        })),
      },
      failures,
    };
  }

  private buildRequestEnv(request: AdmissionRequest): CelValue {
    return {
      uid: request.uid ?? '',
      operation: request.operation,
      namespace: request.namespace ?? '',
      name: request.name ?? '',
      resource: {
        group: request.resource.apiGroup,
        version: request.resource.apiVersion,
        resource: request.resource.resource,
        subresource: request.resource.subresource ?? '',
      },
      userInfo: request.userInfo
        ? {
            username: request.userInfo.username ?? '',
            groups: request.userInfo.groups ?? [],
          }
        : { username: '', groups: [] },
      options: (request.options as CelValue) ?? null,
    };
  }

  private evaluateOnce(
    policy: CompiledPolicy,
    binding: ValidatingAdmissionPolicyBinding,
    request: AdmissionRequest,
    parameter: ParameterObject | undefined,
    parameterKey: ParameterKey | undefined,
  ): { skipped: boolean; failures: FailureEntry[] } {
    const bindingName = binding.metadata!.name!;
    const actions = binding.spec.validationActions ?? ['Deny'];
    const baseEnv: Record<string, CelValue> = {
      object: toCelValue(request.object),
      oldObject: toCelValue(request.oldObject ?? null),
      request: this.buildRequestEnv(request),
      params: (parameter as CelValue) ?? null,
      self: toCelValue(request.object),
    };

    // 3a. Variables (declaration order; later variables may reference earlier ones).
    const variableEnv: Record<string, CelValue> = {};
    for (const variable of policy.variables) {
      try {
        variableEnv[variable.name] = evaluate(variable.expression, {
          ...baseEnv,
          ...variableEnv,
          variables: { ...variableEnv },
        });
      } catch (err) {
        const handled = this.handleExpressionError(
          policy,
          binding,
          err,
          `variable '${variable.name}'`,
          actions,
          parameterKey,
        );
        if (handled.skipped) return { skipped: true, failures: [] };
        return { skipped: false, failures: handled.failures };
      }
    }

    // 3b. matchConditions in order; the first false short-circuits this evaluation.
    const conditionEnv = {
      ...baseEnv,
      ...variableEnv,
      variables: { ...variableEnv },
    };
    for (const condition of policy.matchConditions) {
      let result: CelValue;
      try {
        result = evaluate(condition.expression, conditionEnv);
      } catch (err) {
        const handled = this.handleExpressionError(
          policy,
          binding,
          err,
          `matchCondition '${condition.name}'`,
          actions,
          parameterKey,
        );
        if (handled.skipped) return { skipped: true, failures: [] };
        return { skipped: false, failures: handled.failures };
      }
      if (typeof result !== 'boolean') {
        const handled = this.handleExpressionError(
          policy,
          binding,
          new CelError(
            `matchCondition '${condition.name}' must evaluate to bool, got ${typeof result}`,
          ),
          `matchCondition '${condition.name}'`,
          actions,
          parameterKey,
        );
        if (handled.skipped) return { skipped: true, failures: [] };
        return { skipped: false, failures: handled.failures };
      }
      if (!result) {
        return { skipped: true, failures: [] };
      }
    }

    // 3c. Every validation runs independently; failures never short-circuit later validations.
    const failures: FailureEntry[] = [];
    policy.validations.forEach((validation, index) => {
      let result: CelValue;
      try {
        result = evaluate(validation.expression, conditionEnv);
      } catch (err) {
        const handled = this.handleExpressionError(
          policy,
          binding,
          err,
          `validation[${index}]`,
          actions,
          parameterKey,
        );
        failures.push(...handled.failures);
        return;
      }
      if (typeof result !== 'boolean') {
        const handled = this.handleExpressionError(
          policy,
          binding,
          new CelError(
            `validation[${index}] must evaluate to bool, got ${typeof result}`,
          ),
          `validation[${index}]`,
          actions,
          parameterKey,
        );
        failures.push(...handled.failures);
        return;
      }
      if (!result) {
        failures.push({
          binding: bindingName,
          policy: policy.name,
          validationIndex: index,
          message: this.renderMessage(validation, conditionEnv, policy.name, index),
          actions,
          parameterKey,
        });
      }
    });

    return { skipped: false, failures };
  }

  private handleExpressionError(
    policy: CompiledPolicy,
    binding: ValidatingAdmissionPolicyBinding,
    err: unknown,
    where: string,
    actions: ValidationAction[],
    parameterKey: ParameterKey | undefined,
  ): { skipped: boolean; failures: FailureEntry[] } {
    const message = `${where} evaluation error: ${
      err instanceof Error ? err.message : String(err)
    }`;
    if (policy.failurePolicy === 'Ignore') {
      // Ignore: a failing match condition/variable is treated as non-applicable;
      // a failing validation simply contributes no failure.
      return {
        skipped: where.startsWith('matchCondition') || where.startsWith('variable'),
        failures: [],
      };
    }
    return {
      skipped: false,
      failures: [
        {
          binding: binding.metadata!.name!,
          policy: policy.name,
          validationIndex: -1,
          message,
          actions,
          parameterKey,
        },
      ],
    };
  }

  private renderMessage(
    validation: CompiledValidation,
    env: Record<string, CelValue>,
    policyName: string,
    index: number,
  ): string {
    if (validation.message) return validation.message;
    if (validation.messageExpression) {
      try {
        const rendered = evaluate(validation.messageExpression, env);
        if (typeof rendered === 'string') return rendered;
      } catch {
        // Fall through to the default message when messageExpression itself errors.
      }
    }
    return `ValidatingAdmissionPolicy '${policyName}' failed validation expression ${index}`;
  }
}
