import type { Script, Step } from './model.ts';

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isPositiveInt(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value > 0;
}

function validateDependsOn(
  step: Record<string, unknown>,
  index: number,
  rawSteps: unknown[],
  errors: string[],
): void {
  if (step.dependsOn === undefined) return;
  if (!Array.isArray(step.dependsOn) || step.dependsOn.some((d) => typeof d !== 'string')) {
    errors.push(`step[${index}] (${String(step.id)}): dependsOn must be an array of step ids`);
    return;
  }
  const earlierIds = new Set(
    rawSteps.slice(0, index).filter(isRecord).map((s) => s.id),
  );
  for (const dep of step.dependsOn as string[]) {
    if (dep === step.id) {
      errors.push(`step[${index}] (${dep}): a step cannot depend on itself`);
    } else if (!earlierIds.has(dep)) {
      errors.push(
        `step[${index}] (${String(step.id)}): dependsOn references "${dep}" which is not an earlier step`,
      );
    }
  }
}

function validateStepShape(step: unknown, index: number, nodes: Set<string>, errors: string[]): void {
  if (!isRecord(step)) {
    errors.push(`step[${index}]: must be an object`);
    return;
  }
  if (typeof step.id !== 'string' || step.id.length === 0) {
    errors.push(`step[${index}]: id must be a non-empty string`);
  }
  switch (step.type) {
    case 'delay':
      if (!isPositiveInt(step.ms)) {
        errors.push(`step[${index}] (${String(step.id)}): delay.ms must be a positive integer`);
      }
      break;
    case 'drop': {
      const fromOk = typeof step.from === 'string' && nodes.has(step.from);
      const toOk = typeof step.to === 'string' && nodes.has(step.to);
      if (!fromOk) errors.push(`step[${index}] (${String(step.id)}): drop.from is not a declared node`);
      if (!toOk) errors.push(`step[${index}] (${String(step.id)}): drop.to is not a declared node`);
      if (fromOk && toOk && step.from === step.to) {
        errors.push(`step[${index}] (${String(step.id)}): drop.from and drop.to must differ`);
      }
      if (!isPositiveInt(step.count)) {
        errors.push(`step[${index}] (${String(step.id)}): drop.count must be a positive integer`);
      }
      break;
    }
    case 'restart':
      if (typeof step.node !== 'string' || !nodes.has(step.node)) {
        errors.push(`step[${index}] (${String(step.id)}): restart.node is not a declared node`);
      }
      break;
    default:
      errors.push(`step[${index}] (${String(step.id)}): unknown step type ${JSON.stringify(step.type)}`);
  }
}

/**
 * Returns a list of validation errors; an empty list means the script is
 * executable. A script is executable only when every step is well-formed and
 * every declared dependency refers to a step that appears earlier.
 */
export function validateScript(script: unknown): string[] {
  const errors: string[] = [];
  if (!isRecord(script)) return ['script must be an object'];

  if (!Array.isArray(script.nodes) || script.nodes.length === 0) {
    errors.push('script.nodes must be a non-empty array of node names');
  } else if (script.nodes.some((n) => typeof n !== 'string' || n.length === 0)) {
    errors.push('script.nodes must contain only non-empty strings');
  } else if (new Set(script.nodes).size !== script.nodes.length) {
    errors.push('script.nodes must not contain duplicates');
  }
  const nodes = new Set<string>(Array.isArray(script.nodes) ? (script.nodes as string[]) : []);

  if (!Array.isArray(script.steps)) {
    errors.push('script.steps must be an array');
    return errors;
  }
  const rawSteps = script.steps as unknown[];
  const seenIds = new Set<string>();
  rawSteps.forEach((step, index) => {
    if (isRecord(step) && typeof step.id === 'string') {
      if (seenIds.has(step.id)) errors.push(`step[${index}]: duplicate step id "${step.id}"`);
      seenIds.add(step.id);
    }
    validateStepShape(step, index, nodes, errors);
  });
  rawSteps.forEach((step, index) => {
    if (isRecord(step)) {
      validateDependsOn(step, index, rawSteps, errors);
    }
  });
  return errors;
}

export function parseScript(raw: string): { script?: Script; errors: string[] } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (error) {
    return { errors: [`invalid JSON: ${(error as Error).message}`] };
  }
  const errors = validateScript(parsed);
  return errors.length === 0 ? { script: parsed as Script, errors } : { errors };
}
