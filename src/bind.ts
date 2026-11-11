import type { ParamType } from './ast.ts';
import { QueryStructureError } from './errors.ts';
import type { ParamSlot } from './canonical.ts';

export type BoundValue = string | number | boolean | Date | null;
export type BoundValues = Record<string, BoundValue>;

export function bindValues(slots: readonly ParamSlot[], values: BoundValues): BoundValue[] {
  return slots.map((slot) => {
    if (!Object.hasOwn(values, slot.name)) {
      throw new QueryStructureError(`missing value for parameter: ${slot.name}`);
    }
    const value = values[slot.name];
    assertType(slot.name, slot.paramType, value);
    return value;
  });
}

function assertType(name: string, paramType: ParamType, value: BoundValue): void {
  switch (paramType) {
    case 'string':
      if (typeof value !== 'string') {
        throw new QueryStructureError(`parameter ${name} requires a string`);
      }
      return;
    case 'integer':
      if (typeof value !== 'number' || !Number.isSafeInteger(value)) {
        throw new QueryStructureError(`parameter ${name} requires a safe integer`);
      }
      return;
    case 'number':
      if (typeof value !== 'number' || !Number.isFinite(value)) {
        throw new QueryStructureError(`parameter ${name} requires a finite number`);
      }
      return;
    case 'boolean':
      if (typeof value !== 'boolean') {
        throw new QueryStructureError(`parameter ${name} requires a boolean`);
      }
      return;
    case 'date':
      if (!(value instanceof Date) || Number.isNaN(value.getTime())) {
        throw new QueryStructureError(`parameter ${name} requires a valid Date`);
      }
      return;
    case 'null':
      if (value !== null) {
        throw new QueryStructureError(`parameter ${name} requires null`);
      }
      return;
  }
}
