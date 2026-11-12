import { relativeGeometry } from './geometry.ts';
import { FORBIDDEN_COST } from './hungarian.ts';
import { sharesFormAncestor, type TreeIndex } from './tree.ts';
import type {
  AccessNode,
  CostWeights,
  EdgeCost,
  RelativeDirection,
} from './types.ts';

export interface EdgeContext {
  index: TreeIndex;
  weights: CostWeights;
  preferred: Set<RelativeDirection>;
  includeHidden: boolean;
  /** Controls already consumed by explicit relations. */
  occupiedControls: Set<string>;
}

function roleMatches(label: AccessNode, control: AccessNode): boolean {
  if (!label.expectedControlRoles || label.expectedControlRoles.length === 0) {
    return true;
  }
  return label.expectedControlRoles.includes(control.role);
}

/**
 * Evaluate one label/control edge. Forbidden edges keep a finite sentinel
 * cost (required by the Hungarian solver) and `forbidden: true`.
 */
export function evaluateEdge(
  label: AccessNode,
  control: AccessNode,
  context: EdgeContext,
): EdgeCost {
  const reasons: string[] = [];
  let forbidden = false;

  if (!sharesFormAncestor(context.index, label.id, control.id)) {
    forbidden = true;
    reasons.push('cross-form ancestor: label and control belong to different form/dialog subtrees');
  }
  if (control.hidden) {
    if (context.includeHidden) {
      reasons.push('control marked hidden (includeHidden=true)');
    } else {
      forbidden = true;
      reasons.push('control hidden: excluded from matching');
    }
  }
  if (!control.rect) {
    forbidden = true;
    reasons.push('control has no visible rectangle');
  }
  if (!label.rect) {
    forbidden = true;
    reasons.push('label has no visible rectangle');
  }
  if (context.occupiedControls.has(control.id)) {
    forbidden = true;
    reasons.push('control already bound by an explicit relation');
  }

  const geometry = label.rect && control.rect
    ? relativeGeometry(label.rect, control.rect)
    : null;
  const direction = geometry?.direction ?? 'overlap';
  const preferredDirection = context.preferred.has(direction);
  if (!preferredDirection && geometry) {
    reasons.push(`control is ${direction} of label, preferred directions are not met`);
  }

  const roleMismatch = !roleMatches(label, control);
  if (roleMismatch) {
    reasons.push(
      `role mismatch: control role '${control.role}' not in expected [${label.expectedControlRoles!.join(', ')}]`,
    );
  }

  const rawDistance = geometry?.rawDistance ?? Number.POSITIVE_INFINITY;
  let cost =
    context.weights.distance * (geometry?.rawDistance ?? 0) +
    (preferredDirection ? 0 : context.weights.wrongDirection) +
    (roleMismatch ? context.weights.roleMismatch : 0);
  if (forbidden) {
    cost = FORBIDDEN_COST;
  }

  return {
    labelId: label.id,
    controlId: control.id,
    rawDistance,
    direction,
    preferredDirection,
    roleMismatch,
    cost,
    reasons,
    forbidden,
  };
}
