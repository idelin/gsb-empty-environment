import { Step } from "./types";

export interface ValidationOptions {
  nodes: string[];
  /** Time a node needs after "restart" before it can be touched again. */
  recoveryMs: number;
  maxDelayMs?: number;
}

export interface ValidationResult {
  ok: boolean;
  errors: string[];
}

/**
 * Static executability check for a script. Steps have sequential
 * dependencies: after `restart(N)`, at least `recoveryMs` of accumulated
 * delay must pass before any other step may target N. Deleting such a
 * delay makes the script invalid, and invalid scripts must never be
 * accepted as successful reductions.
 */
export function validateScript(steps: Step[], opts: ValidationOptions): ValidationResult {
  const errors: string[] = [];
  const known = new Set(opts.nodes);
  const downUntil = new Map<string, number>();
  const maxDelay = opts.maxDelayMs ?? 60_000;
  let now = 0;

  const isUp = (node: string): boolean => {
    const until = downUntil.get(node);
    return until === undefined || now >= until;
  };

  steps.forEach((step, index) => {
    const at = `step[${index}]`;
    if (typeof step !== "object" || step === null) {
      errors.push(`${at}: not an object`);
      return;
    }
    const raw = step as unknown as Record<string, unknown>;
    switch (raw.type) {
      case "delay": {
        const ms = raw.ms;
        if (typeof ms !== "number" || !Number.isFinite(ms) || !Number.isInteger(ms) || ms <= 0) {
          errors.push(`${at}: delay.ms must be a positive integer, got ${JSON.stringify(ms)}`);
        } else if (ms > maxDelay) {
          errors.push(`${at}: delay.ms ${ms} exceeds max ${maxDelay}`);
        } else {
          now += ms;
        }
        break;
      }
      case "packet-loss": {
        const node = raw.node;
        const rate = raw.rate;
        if (typeof node !== "string" || !known.has(node)) {
          errors.push(`${at}: unknown node ${JSON.stringify(node)}`);
          break;
        }
        if (typeof rate !== "number" || !Number.isFinite(rate) || rate < 0 || rate > 1) {
          errors.push(`${at}: packet-loss.rate must be in [0, 1], got ${JSON.stringify(rate)}`);
        }
        if (!isUp(node)) {
          errors.push(
            `${at}: node ${node} is still recovering from restart ` +
              `(down until ${downUntil.get(node)}ms, now ${now}ms)`
          );
        }
        break;
      }
      case "restart": {
        const node = raw.node;
        if (typeof node !== "string" || !known.has(node)) {
          errors.push(`${at}: unknown node ${JSON.stringify(node)}`);
          break;
        }
        if (!isUp(node)) {
          errors.push(
            `${at}: node ${node} is still recovering from a previous restart ` +
              `(down until ${downUntil.get(node)}ms, now ${now}ms)`
          );
        }
        downUntil.set(node, now + opts.recoveryMs);
        break;
      }
      default:
        errors.push(`${at}: unknown step type ${JSON.stringify(raw.type)}`);
    }
  });

  return { ok: errors.length === 0, errors };
}
