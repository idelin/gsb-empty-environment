import {
  admitRepairPlan,
  RepairAdmissionError,
  type AdmissionOptions,
  type AdmissionResult,
} from './admission.ts';

export const admitModularRepair = admitRepairPlan;
export const validateModularRepair = admitRepairPlan;
export const checkModularRepairAdmission = admitRepairPlan;
export const admit = admitRepairPlan;

export class ModularRepairAdmission {
  private readonly defaults: Partial<AdmissionOptions>;

  constructor(defaults: Partial<AdmissionOptions> = {}) {
    this.defaults = defaults;
  }

  admit(options: AdmissionOptions): AdmissionResult {
    return admitRepairPlan({ ...this.defaults, ...options });
  }

  review(options: AdmissionOptions): AdmissionResult {
    return this.admit(options);
  }

  check(options: AdmissionOptions): AdmissionResult {
    return this.admit(options);
  }

  assert(options: AdmissionOptions): AdmissionResult {
    const result = this.admit(options);
    if (!result.admitted) throw new RepairAdmissionError(result);
    return result;
  }
}

export function serializeAdmissionResult(result: AdmissionResult): string {
  return JSON.stringify(
    result,
    (_key, value: unknown) => (typeof value === 'bigint' ? value.toString() : value),
    2,
  );
}
