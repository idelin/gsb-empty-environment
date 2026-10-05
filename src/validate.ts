import type { ChargingProblem } from './types';

export class ChargingValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ChargingValidationError';
  }
}

function assertInteger(value: number, what: string): void {
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    throw new ChargingValidationError(`${what} must be an integer, got ${String(value)}`);
  }
}

function assertNonNegative(value: number, what: string): void {
  if (value < 0) {
    throw new ChargingValidationError(`${what} must be >= 0, got ${value}`);
  }
}

/** 校验输入问题；不合法时抛出 ChargingValidationError。 */
export function validateProblem(problem: ChargingProblem): void {
  if (!problem || typeof problem !== 'object') {
    throw new ChargingValidationError('problem must be an object');
  }
  const { slots, vehicles } = problem;
  if (!Array.isArray(slots)) {
    throw new ChargingValidationError('slots must be an array');
  }
  if (!Array.isArray(vehicles)) {
    throw new ChargingValidationError('vehicles must be an array');
  }
  slots.forEach((slot, i) => {
    assertInteger(slot.powerLimit, `slots[${i}].powerLimit`);
    assertNonNegative(slot.powerLimit, `slots[${i}].powerLimit`);
    assertInteger(slot.price, `slots[${i}].price`);
  });
  const seen = new Set<string>();
  vehicles.forEach((v, i) => {
    const at = `vehicles[${i}]`;
    if (!v || typeof v !== 'object') {
      throw new ChargingValidationError(`${at} must be an object`);
    }
    if (typeof v.id !== 'string' || v.id.length === 0) {
      throw new ChargingValidationError(`${at}.id must be a non-empty string`);
    }
    if (seen.has(v.id)) {
      throw new ChargingValidationError(`duplicate vehicle id: ${v.id}`);
    }
    seen.add(v.id);
    assertInteger(v.arrival, `${at}.arrival`);
    assertInteger(v.departure, `${at}.departure`);
    assertInteger(v.demand, `${at}.demand`);
    assertInteger(v.maxPower, `${at}.maxPower`);
    assertNonNegative(v.arrival, `${at}.arrival`);
    assertNonNegative(v.demand, `${at}.demand`);
    assertNonNegative(v.maxPower, `${at}.maxPower`);
    if (v.departure <= v.arrival) {
      throw new ChargingValidationError(
        `${at}.departure (${v.departure}) must be greater than arrival (${v.arrival})`,
      );
    }
    if (v.departure > slots.length) {
      throw new ChargingValidationError(
        `${at}.departure (${v.departure}) exceeds number of slots (${slots.length})`,
      );
    }
    if (v.maxStarts !== undefined) {
      assertInteger(v.maxStarts, `${at}.maxStarts`);
      assertNonNegative(v.maxStarts, `${at}.maxStarts`);
    }
  });
}
