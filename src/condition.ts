import type {
  ConditionNode,
  ConditionOperator,
  EvalContext,
  LeafCondition,
  TriState,
} from './types.ts';

export interface ConditionResult {
  outcome: TriState;
  missingKeys: string[];
}

function lookup(context: EvalContext, key: string): string[] | undefined {
  const principalTag = 'aws:PrincipalTag/';
  const resourceTag = 'aws:ResourceTag/';
  const requestTag = 'aws:RequestTag/';

  if (key.startsWith(principalTag)) {
    const tagKey = key.slice(principalTag.length);
    const value = context.principal.tags?.[tagKey];
    return value === undefined ? undefined : [value];
  }
  if (key.startsWith(resourceTag)) {
    const tagKey = key.slice(resourceTag.length);
    const value = context.resourceTags[tagKey];
    return value === undefined ? undefined : [value];
  }
  if (key.startsWith(requestTag)) {
    const tagKey = key.slice(requestTag.length);
    const value = context.requestTags?.[tagKey];
    return value === undefined ? undefined : [value];
  }

  switch (key) {
    case 'aws:PrincipalAccount':
      return [context.principal.account];
    case 'aws:PrincipalArn':
      return [context.principal.id];
    case 'aws:Action':
      return [context.action];
    case 'aws:ResourceArn':
      return [context.resourceId];
    default: {
      const extra = context.extra?.[key];
      if (extra !== undefined) {
        return Array.isArray(extra) ? extra : [String(extra)];
      }
      const requestValue = context.requestTags?.[key];
      if (requestValue !== undefined) {
        return [requestValue];
      }
      return undefined;
    }
  }
}

function asNumber(value: string | number | boolean): number {
  return typeof value === 'number' ? value : Number(value);
}

function asBool(value: string | number | boolean): boolean {
  if (typeof value === 'boolean') {
    return value;
  }
  return String(value).toLowerCase() === 'true';
}

function isNegativeOperator(operator: ConditionOperator): boolean {
  return operator === 'StringNotEquals' || operator === 'StringNotLike' || operator === 'NumericNotEquals';
}

function globLike(pattern: string, value: string): boolean {
  let regex = '^';
  for (const ch of pattern) {
    if (ch === '*') {
      regex += '.*';
    } else if (ch === '?') {
      regex += '.';
    } else {
      regex += ch.replace(/[.+^${}()|[\]\\]/g, '\\$&');
    }
  }
  regex += '$';
  return new RegExp(regex).test(value);
}

function valueSatisfies(
  operator: ConditionOperator,
  contextValue: string,
  conditionValues: (string | number | boolean)[],
): boolean {
  let matched = false;
  for (const conditionValue of conditionValues) {
    switch (operator) {
      case 'StringEquals':
      case 'StringNotEquals':
        matched ||= contextValue === String(conditionValue);
        break;
      case 'StringLike':
      case 'StringNotLike': {
        const pattern = String(conditionValue);
        matched ||=
          pattern === contextValue ||
          ((pattern.includes('*') || pattern.includes('?')) &&
            globLike(pattern, contextValue));
        break;
      }
      case 'NumericEquals':
      case 'NumericNotEquals':
        matched ||= asNumber(contextValue) === asNumber(conditionValue);
        break;
      case 'NumericLessThan':
        matched ||= asNumber(contextValue) < asNumber(conditionValue);
        break;
      case 'NumericLessThanEquals':
        matched ||= asNumber(contextValue) <= asNumber(conditionValue);
        break;
      case 'NumericGreaterThan':
        matched ||= asNumber(contextValue) > asNumber(conditionValue);
        break;
      case 'NumericGreaterThanEquals':
        matched ||= asNumber(contextValue) >= asNumber(conditionValue);
        break;
      case 'Bool':
        matched ||= asBool(contextValue) === asBool(conditionValue);
        break;
    }
  }
  return isNegativeOperator(operator) ? !matched : matched;
}

function evaluateLeaf(leaf: LeafCondition, context: EvalContext): ConditionResult {
  const substituted: (string | number | boolean)[] = [];
  for (const value of leaf.values) {
    if (typeof value !== 'string' || !value.includes('${')) {
      substituted.push(value);
      continue;
    }
    let missing: string | undefined;
    const rendered = value.replace(/\$\{([^}]+)\}/g, (_, variableKey: string) => {
      const resolved = lookup(context, variableKey);
      if (resolved === undefined || resolved.length === 0) {
        missing = variableKey;
        return '';
      }
      return resolved[0];
    });
    if (missing) {
      return { outcome: null, missingKeys: [missing] };
    }
    substituted.push(rendered);
  }

  const contextValues = lookup(context, leaf.key);
  if (contextValues === undefined) {
    return { outcome: null, missingKeys: [leaf.key] };
  }

  if (contextValues.length === 0 && leaf.quantifier === 'ForAllValues') {
    return { outcome: true, missingKeys: [] };
  }

  const predicate = (contextValue: string) =>
    valueSatisfies(leaf.operator, contextValue, substituted);

  const quantifier = leaf.quantifier ?? 'ForAllValues';
  const outcome =
    quantifier === 'ForAnyValue'
      ? contextValues.some(predicate)
      : contextValues.every(predicate);

  return { outcome, missingKeys: [] };
}

function combine(
  combinator: 'and' | 'or' | 'not',
  results: ConditionResult[],
): ConditionResult {
  const missingKeys = results.flatMap((result) => result.missingKeys);
  const outcomes = results.map((result) => result.outcome);

  let outcome: TriState;
  if (combinator === 'and') {
    if (outcomes.includes(false)) {
      outcome = false;
    } else if (outcomes.includes(null)) {
      outcome = null;
    } else {
      outcome = true;
    }
  } else if (combinator === 'or') {
    if (outcomes.includes(true)) {
      outcome = true;
    } else if (outcomes.includes(null)) {
      outcome = null;
    } else {
      outcome = false;
    }
  } else {
    const only = outcomes[0] ?? null;
    outcome = only === null ? null : !only;
  }

  return { outcome, missingKeys: [...new Set(missingKeys)] };
}

export function evaluateCondition(
  node: ConditionNode | undefined,
  context: EvalContext,
): ConditionResult {
  if (node === undefined) {
    return { outcome: true, missingKeys: [] };
  }
  if (node.kind === 'leaf') {
    return evaluateLeaf(node, context);
  }
  const childResults = node.children.map((child) => evaluateCondition(child, context));
  return combine(node.kind, childResults);
}
