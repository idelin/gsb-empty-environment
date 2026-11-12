import type { AccessNode, ExplicitLabelRelation } from '../index.ts';

export interface Scenario {
  name: string;
  nodes: AccessNode[];
  explicit: ExplicitLabelRelation[];
}

const r = (x: number, y: number, width = 80, height = 24) => ({ x, y, width, height });

/**
 * Two stacked forms, each containing two labels with two same-sized
 * controls. Distance-only matching would happily pair across forms; the
 * form-ancestry rule must forbid those edges.
 */
export function crossFormFixture(): Scenario {
  const nodes: AccessNode[] = [
    { id: 'form-a', role: 'form', parentId: null, rect: r(0, 0, 400, 200) },
    { id: 'form-b', role: 'form', parentId: null, rect: r(0, 240, 400, 200) },
    { id: 'a-label-1', role: 'label', isLabel: true, name: 'A1', parentId: 'form-a', rect: r(10, 20), expectedControlRoles: ['textbox'] },
    { id: 'a-label-2', role: 'label', isLabel: true, name: 'A2', parentId: 'form-a', rect: r(10, 70), expectedControlRoles: ['textbox'] },
    { id: 'a-input-1', role: 'textbox', parentId: 'form-a', rect: r(120, 20) },
    { id: 'a-input-2', role: 'textbox', parentId: 'form-a', rect: r(120, 70) },
    { id: 'b-label-1', role: 'label', isLabel: true, name: 'B1', parentId: 'form-b', rect: r(10, 260), expectedControlRoles: ['textbox'] },
    { id: 'b-label-2', role: 'label', isLabel: true, name: 'B2', parentId: 'form-b', rect: r(10, 310), expectedControlRoles: ['textbox'] },
    { id: 'b-input-1', role: 'textbox', parentId: 'form-b', rect: r(120, 260) },
    { id: 'b-input-2', role: 'textbox', parentId: 'form-b', rect: r(120, 310) },
  ];
  return { name: 'cross-form', nodes, explicit: [] };
}

/** Two labels with identical text; spacing makes one pairing ambiguous. */
export function duplicateLabelFixture(): Scenario {
  const nodes: AccessNode[] = [
    { id: 'dlg', role: 'dialog', parentId: null, rect: r(0, 0, 400, 200) },
    { id: 'dup-label-1', role: 'label', isLabel: true, name: 'Name', parentId: 'dlg', rect: r(10, 20) },
    { id: 'dup-label-2', role: 'label', isLabel: true, name: 'Name', parentId: 'dlg', rect: r(10, 60) },
    // Near-equidistant controls: gap 30 vs 34, the margin is tiny.
    { id: 'dup-input-1', role: 'textbox', parentId: 'dlg', rect: r(120, 20) },
    { id: 'dup-input-2', role: 'textbox', parentId: 'dlg', rect: r(124, 60) },
  ];
  return { name: 'duplicate-labels', nodes, explicit: [] };
}

/** One control is hidden (no rect + hidden flag); it must be excluded. */
export function hiddenControlFixture(): Scenario {
  const nodes: AccessNode[] = [
    { id: 'dlg', role: 'dialog', parentId: null, rect: r(0, 0, 400, 200) },
    { id: 'vis-label', role: 'label', isLabel: true, name: 'Visible', parentId: 'dlg', rect: r(10, 20) },
    { id: 'hid-label', role: 'label', isLabel: true, name: 'Hidden', parentId: 'dlg', rect: r(10, 70) },
    { id: 'vis-input', role: 'textbox', parentId: 'dlg', rect: r(120, 20) },
    { id: 'hid-input', role: 'textbox', parentId: 'dlg', hidden: true, rect: r(120, 70) },
  ];
  return { name: 'hidden-control', nodes, explicit: [] };
}

/** Nested modal dialog opens inside a parent form; boundaries differ. */
export function nestedDialogFixture(): Scenario {
  const nodes: AccessNode[] = [
    { id: 'outer-form', role: 'form', parentId: null, rect: r(0, 0, 500, 400) },
    { id: 'outer-label', role: 'label', isLabel: true, name: 'Outer', parentId: 'outer-form', rect: r(10, 20) },
    { id: 'outer-input', role: 'textbox', parentId: 'outer-form', rect: r(120, 20) },
    { id: 'modal', role: 'dialog', parentId: 'outer-form', rect: r(20, 80, 460, 300) },
    { id: 'modal-label', role: 'label', isLabel: true, name: 'Modal', parentId: 'modal', rect: r(30, 100) },
    { id: 'modal-input', role: 'textbox', parentId: 'modal', rect: r(150, 100) },
  ];
  return { name: 'nested-dialog', nodes, explicit: [] };
}

/** Explicit `for` relation must win even when geometry prefers another. */
export function explicitPriorityFixture(): Scenario {
  const nodes: AccessNode[] = [
    { id: 'dlg', role: 'dialog', parentId: null, rect: r(0, 0, 400, 200) },
    { id: 'exp-label', role: 'label', isLabel: true, name: 'Token', parentId: 'dlg', rect: r(10, 20) },
    { id: 'near-input', role: 'textbox', parentId: 'dlg', rect: r(120, 20) },
    // Farther away but explicitly referenced.
    { id: 'far-input', role: 'textbox', parentId: 'dlg', rect: r(300, 120) },
  ];
  const explicit: ExplicitLabelRelation[] = [
    { labelId: 'exp-label', controlId: 'far-input', kind: 'for' },
  ];
  return { name: 'explicit-priority', nodes, explicit };
}

export function allScenarios(): Scenario[] {
  return [
    crossFormFixture(),
    duplicateLabelFixture(),
    nestedDialogFixture(),
    hiddenControlFixture(),
    explicitPriorityFixture(),
  ];
}
