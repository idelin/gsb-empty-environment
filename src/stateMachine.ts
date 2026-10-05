import { Phase } from './phases';
import { InvalidTransitionError } from './errors';

/** Legal phase transitions. Terminal phases map to the empty set. */
const TRANSITIONS: Readonly<Record<Phase, readonly Phase[]>> = {
  idle: ['preparing'],
  preparing: ['submitting', 'cancelled', 'failed'],
  submitting: ['cleaning', 'in-doubt'],
  'in-doubt': ['cleaning'],
  cleaning: ['completed'],
  completed: [],
  cancelled: [],
  failed: [],
};

export function canTransition(from: Phase, to: Phase): boolean {
  return (TRANSITIONS[from] as readonly Phase[]).includes(to);
}

export function assertTransition(from: Phase, to: Phase): void {
  if (!canTransition(from, to)) throw new InvalidTransitionError(from, to);
}
