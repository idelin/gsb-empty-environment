export type Decision = 'Permit' | 'Deny' | 'NotApplicable' | 'Indeterminate';

export const Decisions: Readonly<Record<string, Decision>> = {
  Permit: 'Permit',
  Deny: 'Deny',
  NotApplicable: 'NotApplicable',
  Indeterminate: 'Indeterminate',
};
