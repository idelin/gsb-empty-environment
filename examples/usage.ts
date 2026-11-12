import { bindForms, type AccessNode, type ExplicitLabelRelation } from '../index.ts';

/**
 * Minimal end-to-end example for an automation host: build the
 * accessibility snapshot, pass explicit relations and tuning, and gate
 * data entry on `accepted` plus the per-field evidence trail.
 */
const nodes: AccessNode[] = [
  { id: 'dialog', role: 'dialog', parentId: null, rect: { x: 0, y: 0, width: 360, height: 180 } },
  {
    id: 'name-label',
    role: 'label',
    isLabel: true,
    name: 'Full name',
    parentId: 'dialog',
    rect: { x: 16, y: 24, width: 90, height: 24 },
    expectedControlRoles: ['textbox'],
  },
  {
    id: 'name-input',
    role: 'textbox',
    parentId: 'dialog',
    rect: { x: 130, y: 24, width: 200, height: 28 },
  },
  {
    id: 'agree-label',
    role: 'label',
    isLabel: true,
    name: 'Subscribe',
    parentId: 'dialog',
    rect: { x: 16, y: 80, width: 90, height: 24 },
    expectedControlRoles: ['checkbox'],
  },
  { id: 'agree-check', role: 'checkbox', parentId: 'dialog', rect: { x: 130, y: 82, width: 20, height: 20 } },
];

// Explicit relations win first; remaining labels use global min-cost matching.
const explicit: ExplicitLabelRelation[] = [
  { labelId: 'name-label', controlId: 'name-input', kind: 'labelledby' },
];

const result = bindForms(nodes, explicit, {
  ambiguityThreshold: 5,
  weights: { distance: 1, wrongDirection: 100, roleMismatch: 50 },
});

for (const field of result.fields) {
  console.log(JSON.stringify({
    label: field.labelName,
    status: field.status,
    source: field.source,
    control: field.controlId,
    cost: field.cost,
    margin: field.margin,
    reasons: field.reasons,
  }));
}

if (!result.accepted) {
  // Refuse to type into the dialog: an ambiguous or unbound field exists.
  console.error('Binding rejected:', result.diagnostics);
  process.exitCode = 1;
}
