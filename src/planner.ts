import { type RegistrationPlan, planRegistration, type PlanRegistrationInput } from './scope.ts';
import {
  type ScopeRelation,
  compareScopes,
  normalizeUrl,
  scopeContains,
} from './url.ts';

export interface PlannedRegistration {
  readonly id: string;
  readonly origin: string;
  readonly scope: string;
  readonly plan: RegistrationPlan;
}

export interface OverlapReport {
  readonly existingId: string;
  readonly existingScope: string;
  readonly relation: ScopeRelation;
}

export interface RegistrationDecision {
  readonly action: 'create' | 'update';
  readonly registration: PlannedRegistration;
  readonly overlaps: readonly OverlapReport[];
  readonly supersededId: string | null;
}

export class RegistrationPlanner {
  private readonly registrations = new Map<string, PlannedRegistration>();
  private counter = 0;

  register(input: PlanRegistrationInput): RegistrationDecision {
    const plan = planRegistration(input);

    const overlaps: OverlapReport[] = [];
    let supersededId: string | null = null;

    for (const existing of this.registrations.values()) {
      if (existing.origin !== plan.origin) {
        continue;
      }
      const relation = compareScopes(
        { scopePath: plan.scope, scopeSegments: plan.scopeSegments },
        { scopePath: existing.scope, scopeSegments: existing.plan.scopeSegments },
      );
      if (relation !== 'disjoint') {
        overlaps.push({
          existingId: existing.id,
          existingScope: existing.scope,
          relation,
        });
      }
      if (relation === 'equal') {
        supersededId = existing.id;
      }
    }

    const id = `reg-${++this.counter}`;
    const registration: PlannedRegistration = {
      id,
      origin: plan.origin,
      scope: plan.scope,
      plan,
    };

    if (supersededId !== null) {
      this.registrations.delete(supersededId);
    }
    this.registrations.set(id, registration);

    return {
      action: supersededId === null ? 'create' : 'update',
      registration,
      overlaps,
      supersededId,
    };
  }

  findRegistration(urlInput: string, base?: string): PlannedRegistration | null {
    const target = normalizeUrl(urlInput, base);
    let best: PlannedRegistration | null = null;

    for (const registration of this.registrations.values()) {
      if (registration.origin !== target.origin) {
        continue;
      }
      if (!scopeContains(
        {
          scopePath: registration.scope,
          scopeSegments: registration.plan.scopeSegments,
        },
        target,
      )) {
        continue;
      }
      if (
        best === null ||
        registration.plan.scopeSegments.length > best.plan.scopeSegments.length
      ) {
        best = registration;
      }
    }

    return best;
  }

  list(): readonly PlannedRegistration[] {
    return [...this.registrations.values()];
  }
}
