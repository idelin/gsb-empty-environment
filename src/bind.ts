import { evaluateEdge } from './cost.ts';
import { FORBIDDEN_COST, minCostAssignmentAnyShape, type Assignment } from './hungarian.ts';
import { indexTree, type TreeIndex } from './tree.ts';
import {
  DEFAULT_AMBIGUITY_THRESHOLD,
  DEFAULT_FORM_ROLES,
  DEFAULT_PREFERRED_DIRECTIONS,
  DEFAULT_WEIGHTS,
  type AccessNode,
  type BindOptions,
  type BindingResult,
  type CostWeights,
  type EdgeCost,
  type ExplicitLabelRelation,
  type FieldBinding,
  type RelativeDirection,
} from './types.ts';

function resolveWeights(input?: Partial<CostWeights>): CostWeights {
  return { ...DEFAULT_WEIGHTS, ...(input ?? {}) };
}

/**
 * Cheapest full assignment that avoids one specific edge, used to derive
 * the per-field margin. Returns Infinity when no alternative exists.
 */
function assignmentWithoutEdge(
  costs: number[][],
  blockedRow: number,
  blockedCol: number,
): number {
  const copy = costs.map((row) => [...row]);
  copy[blockedRow][blockedCol] = FORBIDDEN_COST;
  const alternative = minCostAssignmentAnyShape(copy);
  return alternative.feasible ? alternative.cost : Number.POSITIVE_INFINITY;
}

export function bindForms(
  nodes: AccessNode[],
  explicitRelations: ExplicitLabelRelation[] = [],
  options: BindOptions = {},
): BindingResult {
  const weights = resolveWeights(options.weights);
  const preferred = new Set<RelativeDirection>(
    options.preferredDirections ?? DEFAULT_PREFERRED_DIRECTIONS,
  );
  const threshold = options.ambiguityThreshold ?? DEFAULT_AMBIGUITY_THRESHOLD;
  const formRoles = options.formRoles ?? DEFAULT_FORM_ROLES;
  const includeHidden = options.includeHidden ?? false;
  const diagnostics: string[] = [];

  const index: TreeIndex = indexTree(nodes, formRoles);
  const fields: FieldBinding[] = [];
  const labels = nodes.filter((node) => node.isLabel);
  const formRoleSet = new Set(formRoles);
  const controls = nodes.filter(
    (node) => !node.isLabel && !formRoleSet.has(node.role),
  );

  const explicitLabelIds = new Set<string>();
  const occupiedControls = new Set<string>();

  // --- Phase 1: explicit relations take priority ------------------------
  for (const relation of explicitRelations) {
    const label = index.nodes.get(relation.labelId);
    const control = index.nodes.get(relation.controlId);
    const field: FieldBinding = {
      labelId: relation.labelId,
      labelName: label?.name ?? null,
      status: 'unbound',
      candidates: [],
      reasons: [],
    };

    if (!label) {
      field.reasons.push(`explicit relation references unknown label ${relation.labelId}`);
      diagnostics.push(field.reasons[field.reasons.length - 1]);
      fields.push(field);
      continue;
    }
    if (!label.isLabel) {
      field.reasons.push(`explicit relation source ${relation.labelId} is not marked isLabel`);
    }
    if (!control) {
      field.reasons.push(`explicit relation references unknown control ${relation.controlId}`);
      fields.push(field);
      diagnostics.push(field.reasons[field.reasons.length - 1]);
      continue;
    }
    if (occupiedControls.has(control.id)) {
      field.reasons.push(
        `explicit control ${control.id} already claimed by another explicit label`,
      );
      fields.push(field);
      diagnostics.push(field.reasons[field.reasons.length - 1]);
      continue;
    }

    const edge = evaluateEdge(label, control, {
      index,
      weights,
      preferred,
      includeHidden,
      occupiedControls,
    });
    field.candidates.push(edge);
    field.edge = edge;

    if (edge.forbidden) {
      field.reasons.push(...edge.reasons);
      field.reasons.push('explicit relation rejected; label falls back to implicit matching');
      diagnostics.push(
        `explicit relation ${relation.labelId} -> ${relation.controlId} rejected (${edge.reasons.join('; ')})`,
      );
      fields.push(field);
      continue;
    }

    explicitLabelIds.add(label.id);
    occupiedControls.add(control.id);
    field.status = 'bound';
    field.source = 'explicit';
    field.controlId = control.id;
    field.controlRole = control.role;
    field.cost = edge.cost;
    field.explicitKind = relation.kind;
    field.reasons.push(
      `explicit ${relation.kind ?? 'relation'} binds label to control within form ${index.formRoot.get(control.id) ?? '(no form)'}`,
    );
    fields.push(field);
  }

  // --- Phase 2: global min-cost one-to-one matching for the remainder ---
  const implicitLabels = labels.filter((label) => !explicitLabelIds.has(label.id));
  const availableControls = controls.filter((control) => !occupiedControls.has(control.id));

  const context = { index, weights, preferred, includeHidden, occupiedControls };
  const edges: EdgeCost[][] = implicitLabels.map((label) =>
    availableControls.map((control) => evaluateEdge(label, control, context)),
  );
  const costs = edges.map((row) => row.map((edge) => edge.cost));

  const noControlsAvailable = availableControls.length === 0 && implicitLabels.length > 0;
  const assignment: Assignment = noControlsAvailable
    ? { rowToCol: implicitLabels.map(() => -1), cost: Number.POSITIVE_INFINITY, feasible: false }
    : minCostAssignmentAnyShape(costs);
  const feasible =
    !noControlsAvailable &&
    assignment.feasible &&
    implicitLabels.every((_, row) => assignment.rowToCol[row] >= 0);

  // Second-best full assignment: rerun with each chosen edge blocked and
  // take the cheapest strictly different result.
  let secondBestCost = Number.POSITIVE_INFINITY;
  if (feasible) {
    for (let row = 0; row < implicitLabels.length; row += 1) {
      const col = assignment.rowToCol[row];
      const alternative = assignmentWithoutEdge(costs, row, col);
      if (alternative < secondBestCost && alternative > assignment.cost + 1e-9) {
        secondBestCost = alternative;
      }
    }
  }
  const globalMargin = feasible && Number.isFinite(secondBestCost)
    ? secondBestCost - assignment.cost
    : null;

  implicitLabels.forEach((label, row) => {
    const sortedCandidates = [...edges[row]].sort((a, b) => a.cost - b.cost);
    const field: FieldBinding = {
      labelId: label.id,
      labelName: label.name ?? null,
      status: 'unbound',
      source: 'implicit',
      candidates: sortedCandidates,
      reasons: [],
    };

    const col = assignment.rowToCol[row];
    if (!feasible || col < 0) {
      field.reasons.push('no feasible one-to-one assignment: every allowed edge is forbidden');
      diagnostics.push(`label ${label.id} could not be matched to any control`);
      fields.push(field);
      return;
    }

    const chosen = edges[row][col];
    const alternativeCost = assignmentWithoutEdge(costs, row, col);
    const margin = Number.isFinite(alternativeCost) ? alternativeCost - chosen.cost : null;

    field.edge = chosen;
    field.controlId = chosen.controlId;
    field.controlRole = availableControls[col].role;
    field.cost = chosen.cost;
    field.secondBestCost = Number.isFinite(alternativeCost) ? alternativeCost : undefined;
    field.margin = margin ?? undefined;
    field.reasons.push(
      `implicit min-cost match: ${chosen.direction} geometry raw=${chosen.rawDistance.toFixed(2)} weighted=${chosen.cost.toFixed(2)}`,
    );
    field.reasons.push(...chosen.reasons);
    field.status = margin !== null && margin < threshold ? 'ambiguous' : 'bound';
    if (field.status === 'ambiguous') {
      field.reasons.push(
        `ambiguous: margin ${margin!.toFixed(2)} below threshold ${threshold}`,
      );
    }
    fields.push(field);
  });

  // Stable field ordering: tree order of labels.
  const labelOrder = new Map(labels.map((label, position) => [label.id, position]));
  fields.sort((a, b) => (labelOrder.get(a.labelId) ?? 0) - (labelOrder.get(b.labelId) ?? 0));

  const ambiguousFields = fields.some((field) => field.status !== 'bound');
  const globalAmbiguous = globalMargin !== null && globalMargin < threshold;
  const accepted = feasible && !ambiguousFields && !globalAmbiguous;

  if (globalAmbiguous) {
    diagnostics.push(
      `global assignment margin ${globalMargin!.toFixed(2)} below threshold ${threshold}`,
    );
  }

  return {
    accepted,
    bestCost: feasible ? assignment.cost : null,
    secondBestCost: Number.isFinite(secondBestCost) ? secondBestCost : null,
    globalMargin,
    threshold,
    fields,
    diagnostics,
  };
}
