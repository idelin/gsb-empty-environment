import {
  applyAffine,
  type Affine,
  type Edges,
  multiply,
  type Point,
  pointInConvexPolygon,
  pointInRect,
  pointInViewport,
  type Rect,
  type Size,
  transformCorners,
  translation,
} from "./geometry.ts";

export interface DocumentHandle {
  readonly documentId: string;
}

export interface FrameElementHandle {
  readonly elementId: string;
}

export interface FrameHandle {
  readonly frameId: string;
  readonly document: DocumentHandle;
}

export interface ElementHandle {
  readonly elementId: string;
}

export interface DocumentSnapshot {
  readonly alive: boolean;
  readonly generation: number;
  readonly viewport: Size;
  readonly scrollX: number;
  readonly scrollY: number;
}

export interface FramePlacement {
  /** Border box in the parent document's layout coordinates. */
  readonly rect: Rect;
  readonly border: Edges;
  readonly padding: Edges;
  /** CSS transform applied around transformOrigin in border-box coordinates. */
  readonly transform: Affine;
  readonly transformOrigin: Point;
}

export interface FrameElementSnapshot {
  readonly alive: boolean;
  readonly elementId: string;
  readonly ownerDocument: DocumentHandle;
  readonly frame: FrameHandle | null;
  readonly placement: FramePlacement;
  readonly rendered: boolean;
  /** Optional ancestor overflow clip in the parent document's layout coordinates. */
  readonly clipInParentDocument: Rect | null;
}

export interface ElementSnapshot {
  readonly alive: boolean;
  readonly elementId: string;
  readonly ownerDocument: DocumentHandle;
  readonly rendered: boolean;
  /** Element-local border box, normally (0, 0, width, height). */
  readonly localBounds: Rect;
  /** Maps element-local coordinates to document layout coordinates. */
  readonly localToDocument: Affine;
  /** Optional ancestor overflow clip in document layout coordinates. */
  readonly clipInDocument: Rect | null;
}

export interface ClickRequest {
  readonly target: ElementHandle;
  readonly frame: FrameHandle | null;
  /** Viewport coordinates of the top-level document. */
  readonly point: Point;
  readonly guard: ExecutionGuard;
}

export interface ExecutionGuard {
  /** Must run synchronously immediately before the side effect. */
  assertUnchanged(): void;
}

/**
 * Synchronous host boundary. Keeping snapshot validation and dispatch in one
 * turn avoids browser-event-loop gaps between them; dispatchClick still invokes
 * the guard immediately before recording the click for adapters that can race.
 */
export interface AutomationHost {
  rootDocument(): DocumentHandle;
  snapshotDocument(document: DocumentHandle): DocumentSnapshot;
  findFrameByName(parent: DocumentHandle, name: string): FrameElementHandle | null;
  snapshotFrameElement(element: FrameElementHandle): FrameElementSnapshot;
  findElementById(document: DocumentHandle, elementId: string): ElementHandle | null;
  snapshotElement(element: ElementHandle): ElementSnapshot;
  dispatchClick(request: ClickRequest): void;
}

export interface FrameLayerBinding {
  readonly index: number;
  readonly name: string;
  readonly parentDocument: DocumentHandle;
  readonly parentDocumentGeneration: number;
  readonly iframeElement: FrameElementHandle;
  readonly iframeElementId: string;
  readonly frame: FrameHandle;
  readonly frameId: string;
  readonly childDocument: DocumentHandle;
  readonly childDocumentGeneration: number;
}

export interface TargetBinding {
  readonly element: ElementHandle;
  readonly elementId: string;
  readonly document: DocumentHandle;
  readonly documentGeneration: number;
  /** Element-local point, before the element's own transform. */
  readonly anchor: Point;
}

export interface OperationPlan {
  readonly version: 1;
  readonly topDocument: DocumentHandle;
  readonly topDocumentGeneration: number;
  /** Outer-to-inner frame chain captured at planning time. */
  readonly layers: readonly FrameLayerBinding[];
  readonly target: TargetBinding;
}

export interface CapturePlanRequest {
  /** Names are resolved only while capturing the plan. */
  readonly framePath: readonly string[];
  readonly targetElementId: string;
  /** Defaults to the center of the target's local border box. */
  readonly anchor?: Point;
}

export interface CapturedOperationPlan {
  readonly plan: OperationPlan;
  readonly initialClickPoint: Point;
}

export interface ExecutionReceipt {
  readonly plan: OperationPlan;
  readonly targetId: string;
  readonly frameId: string | null;
  readonly clickedAt: Point;
}

export type PlanRejectionCode =
  | "TOP_DOCUMENT_CHANGED"
  | "FRAME_NOT_FOUND"
  | "FRAME_ELEMENT_DETACHED"
  | "FRAME_ELEMENT_REPLACED"
  | "FRAME_NOT_RENDERED"
  | "FRAME_INSTANCE_CHANGED"
  | "FRAME_DOCUMENT_CHANGED"
  | "TARGET_NOT_FOUND"
  | "TARGET_ELEMENT_REMOVED"
  | "TARGET_DOCUMENT_CHANGED"
  | "TARGET_NOT_RENDERED"
  | "TARGET_POINT_OUTSIDE_BOUNDS"
  | "TARGET_POINT_CLIPPED"
  | "POINT_OUTSIDE_VIEWPORT"
  | "FRAME_POINT_CLIPPED"
  | "INVALID_PLAN"
  | "EXECUTION_RACE_DETECTED";

export class OperationPlanRejectedError extends Error {
  readonly code: PlanRejectionCode;
  readonly layerIndex: number | undefined;

  constructor(code: PlanRejectionCode, message: string, layerIndex?: number, cause?: unknown) {
    super(message, cause === undefined ? undefined : { cause });
    this.name = "OperationPlanRejectedError";
    this.code = code;
    this.layerIndex = layerIndex;
  }
}

export function elementLocalToDocument(
  rect: Rect,
  transform: Affine,
  transformOrigin: Point,
): Affine {
  const aroundOrigin = multiply(
    translation(transformOrigin.x, transformOrigin.y),
    multiply(transform, translation(-transformOrigin.x, -transformOrigin.y)),
  );
  return multiply(translation(rect.x, rect.y), aroundOrigin);
}

export function frameContentToParentLayout(placement: FramePlacement): Affine {
  const contentOffset = translation(
    placement.border.left + placement.padding.left,
    placement.border.top + placement.padding.top,
  );
  const aroundOrigin = multiply(
    translation(placement.transformOrigin.x, placement.transformOrigin.y),
    multiply(placement.transform, translation(-placement.transformOrigin.x, -placement.transformOrigin.y)),
  );
  return multiply(
    translation(placement.rect.x, placement.rect.y),
    multiply(aroundOrigin, contentOffset),
  );
}

function reject(
  code: PlanRejectionCode,
  message: string,
  layerIndex?: number,
  cause?: unknown,
): never {
  throw new OperationPlanRejectedError(code, message, layerIndex, cause);
}

function idsEqual(first: DocumentHandle, second: DocumentHandle): boolean {
  return first.documentId === second.documentId;
}

interface LayerResolution {
  readonly binding: FrameLayerBinding;
  readonly parentSnapshot: DocumentSnapshot;
  readonly elementSnapshot: FrameElementSnapshot;
  readonly childSnapshot: DocumentSnapshot;
}

interface PlanResolution {
  readonly point: Point;
  readonly fingerprint: string;
  readonly layers: readonly LayerResolution[];
  readonly targetSnapshot: ElementSnapshot;
}

function assertTargetPoint(
  document: DocumentSnapshot,
  target: ElementSnapshot,
  anchorInDocument: Point,
): Point {
  if (target.clipInDocument !== null && !pointInRect(anchorInDocument, target.clipInDocument)) {
    reject("TARGET_POINT_CLIPPED", "Target anchor is outside the target's visible clip");
  }

  const viewportPoint = {
    x: anchorInDocument.x - document.scrollX,
    y: anchorInDocument.y - document.scrollY,
  };
  if (!pointInViewport(viewportPoint, document.viewport)) {
    reject("POINT_OUTSIDE_VIEWPORT", "Target anchor is outside the document viewport");
  }
  return viewportPoint;
}

function fingerprintResolution(
  rootSnapshot: DocumentSnapshot,
  layers: readonly LayerResolution[],
  target: ElementSnapshot,
): string {
  return JSON.stringify({
    root: [
      rootSnapshot.alive,
      rootSnapshot.generation,
      rootSnapshot.viewport.width,
      rootSnapshot.viewport.height,
      rootSnapshot.scrollX,
      rootSnapshot.scrollY,
    ],
    layers: layers.map((layer) => [
      layer.binding.iframeElementId,
      layer.binding.frameId,
      layer.binding.childDocument.documentId,
      layer.parentSnapshot.generation,
      layer.childSnapshot.alive,
      layer.childSnapshot.generation,
      layer.childSnapshot.viewport.width,
      layer.childSnapshot.viewport.height,
      layer.childSnapshot.scrollX,
      layer.childSnapshot.scrollY,
      layer.elementSnapshot.rendered,
      layer.elementSnapshot.placement.rect.x,
      layer.elementSnapshot.placement.rect.y,
      layer.elementSnapshot.placement.rect.width,
      layer.elementSnapshot.placement.rect.height,
      layer.elementSnapshot.placement.border.top,
      layer.elementSnapshot.placement.border.right,
      layer.elementSnapshot.placement.border.bottom,
      layer.elementSnapshot.placement.border.left,
      layer.elementSnapshot.placement.padding.top,
      layer.elementSnapshot.placement.padding.right,
      layer.elementSnapshot.placement.padding.bottom,
      layer.elementSnapshot.placement.padding.left,
      layer.elementSnapshot.placement.transform.a,
      layer.elementSnapshot.placement.transform.b,
      layer.elementSnapshot.placement.transform.c,
      layer.elementSnapshot.placement.transform.d,
      layer.elementSnapshot.placement.transform.e,
      layer.elementSnapshot.placement.transform.f,
      layer.elementSnapshot.placement.transformOrigin.x,
      layer.elementSnapshot.placement.transformOrigin.y,
      layer.elementSnapshot.clipInParentDocument?.x ?? null,
      layer.elementSnapshot.clipInParentDocument?.y ?? null,
      layer.elementSnapshot.clipInParentDocument?.width ?? null,
      layer.elementSnapshot.clipInParentDocument?.height ?? null,
    ]),
    target: [
      target.elementId,
      target.ownerDocument.documentId,
      target.rendered,
      target.localBounds.x,
      target.localBounds.y,
      target.localBounds.width,
      target.localBounds.height,
      target.localToDocument.a,
      target.localToDocument.b,
      target.localToDocument.c,
      target.localToDocument.d,
      target.localToDocument.e,
      target.localToDocument.f,
      target.clipInDocument?.x ?? null,
      target.clipInDocument?.y ?? null,
      target.clipInDocument?.width ?? null,
      target.clipInDocument?.height ?? null,
    ],
  });
}

function resolvePlan(host: AutomationHost, plan: OperationPlan): PlanResolution {
  if (plan.version !== 1) {
    reject("INVALID_PLAN", `Unsupported operation plan version: ${String(plan.version)}`);
  }

  const rootSnapshot = host.snapshotDocument(plan.topDocument);
  if (!rootSnapshot.alive || rootSnapshot.generation !== plan.topDocumentGeneration) {
    reject("TOP_DOCUMENT_CHANGED", "Top-level document generation changed");
  }

  let parentDocument = plan.topDocument;
  let parentSnapshot = rootSnapshot;
  const layerResolutions: LayerResolution[] = [];

  for (const binding of plan.layers) {
    if (!idsEqual(binding.parentDocument, parentDocument)) {
      reject("INVALID_PLAN", "Frame layer is not attached to the preceding layer", binding.index);
    }
    if (binding.parentDocumentGeneration !== parentSnapshot.generation) {
      reject("TOP_DOCUMENT_CHANGED", "Parent document generation changed", binding.index);
    }

    const elementSnapshot = host.snapshotFrameElement(binding.iframeElement);
    if (!elementSnapshot.alive) {
      reject("FRAME_ELEMENT_DETACHED", `Frame element "${binding.name}" is detached`, binding.index);
    }
    if (elementSnapshot.elementId !== binding.iframeElementId) {
      reject("FRAME_ELEMENT_REPLACED", `Frame element "${binding.name}" was replaced`, binding.index);
    }
    if (!idsEqual(elementSnapshot.ownerDocument, parentDocument)) {
      reject("FRAME_ELEMENT_REPLACED", `Frame element "${binding.name}" moved to another document`, binding.index);
    }
    if (!elementSnapshot.rendered) {
      reject("FRAME_NOT_RENDERED", `Frame element "${binding.name}" is not rendered`, binding.index);
    }
    if (elementSnapshot.frame === null) {
      reject("FRAME_ELEMENT_DETACHED", `Frame element "${binding.name}" has no frame instance`, binding.index);
    }
    if (elementSnapshot.frame.frameId !== binding.frameId) {
      reject("FRAME_INSTANCE_CHANGED", `Frame instance for "${binding.name}" changed`, binding.index);
    }
    if (!idsEqual(elementSnapshot.frame.document, binding.childDocument)) {
      reject("FRAME_DOCUMENT_CHANGED", `Frame document for "${binding.name}" was replaced`, binding.index);
    }

    const childSnapshot = host.snapshotDocument(binding.childDocument);
    if (!childSnapshot.alive || childSnapshot.generation !== binding.childDocumentGeneration) {
      reject("FRAME_DOCUMENT_CHANGED", `Frame document for "${binding.name}" changed`, binding.index);
    }
    if (childSnapshot.viewport.width <= 0 || childSnapshot.viewport.height <= 0) {
      reject("FRAME_POINT_CLIPPED", `Frame "${binding.name}" has an empty viewport`, binding.index);
    }

    layerResolutions.push({
      binding,
      parentSnapshot,
      elementSnapshot,
      childSnapshot,
    });
    parentDocument = binding.childDocument;
    parentSnapshot = childSnapshot;
  }

  const targetSnapshot = host.snapshotElement(plan.target.element);
  if (!targetSnapshot.alive) {
    reject("TARGET_ELEMENT_REMOVED", "Target element was removed or replaced");
  }
  if (targetSnapshot.elementId !== plan.target.elementId) {
    reject("TARGET_ELEMENT_REMOVED", "Target element identity changed");
  }
  if (!idsEqual(targetSnapshot.ownerDocument, parentDocument)) {
    reject("TARGET_DOCUMENT_CHANGED", "Target element moved to another document");
  }
  if (targetSnapshot.ownerDocument.documentId !== plan.target.document.documentId) {
    reject("TARGET_DOCUMENT_CHANGED", "Target document handle changed");
  }
  if (
    parentSnapshot.generation !== plan.target.documentGeneration ||
    targetSnapshot.ownerDocument.documentId !== plan.target.document.documentId
  ) {
    reject("TARGET_DOCUMENT_CHANGED", "Target document generation changed");
  }
  if (!targetSnapshot.rendered) {
    reject("TARGET_NOT_RENDERED", "Target element is not rendered");
  }
  if (!pointInRect(plan.target.anchor, targetSnapshot.localBounds)) {
    reject("TARGET_POINT_OUTSIDE_BOUNDS", "Target anchor is outside the element's local bounds");
  }

  const anchorInDocument = applyAffine(targetSnapshot.localToDocument, plan.target.anchor);
  let point = assertTargetPoint(parentSnapshot, targetSnapshot, anchorInDocument);

  for (let index = layerResolutions.length - 1; index >= 0; index -= 1) {
    const layer = layerResolutions[index];
    const visibleElementClip = layer.elementSnapshot.clipInParentDocument;
    if (
      visibleElementClip !== null &&
      (visibleElementClip.width <= 0 || visibleElementClip.height <= 0)
    ) {
      reject("FRAME_POINT_CLIPPED", `Frame "${layer.binding.name}" is fully clipped`, index);
    }
    if (!pointInViewport(point, layer.childSnapshot.viewport)) {
      reject("POINT_OUTSIDE_VIEWPORT", `Point is outside frame "${layer.binding.name}" viewport`, index);
    }

    const contentToParent = frameContentToParentLayout(layer.elementSnapshot.placement);
    const parentLayoutPoint = applyAffine(contentToParent, point);
    const contentQuad = transformCorners(contentToParent, {
      x: 0,
      y: 0,
      width: layer.childSnapshot.viewport.width,
      height: layer.childSnapshot.viewport.height,
    });
    if (!pointInConvexPolygon(parentLayoutPoint, contentQuad)) {
      reject("FRAME_POINT_CLIPPED", `Point is outside frame "${layer.binding.name}" content box`, index);
    }
    if (visibleElementClip !== null && !pointInRect(parentLayoutPoint, visibleElementClip)) {
      reject("FRAME_POINT_CLIPPED", `Point is outside frame "${layer.binding.name}" visible clip`, index);
    }

    const parentViewportPoint = {
      x: parentLayoutPoint.x - layer.parentSnapshot.scrollX,
      y: parentLayoutPoint.y - layer.parentSnapshot.scrollY,
    };
    if (!pointInViewport(parentViewportPoint, layer.parentSnapshot.viewport)) {
      reject("POINT_OUTSIDE_VIEWPORT", `Point is outside parent viewport above "${layer.binding.name}"`, index);
    }
    point = parentViewportPoint;
  }

  return {
    point,
    fingerprint: fingerprintResolution(rootSnapshot, layerResolutions, targetSnapshot),
    layers: layerResolutions,
    targetSnapshot,
  };
}

export function captureOperationPlan(
  host: AutomationHost,
  request: CapturePlanRequest,
): CapturedOperationPlan {
  const topDocument = host.rootDocument();
  const rootSnapshot = host.snapshotDocument(topDocument);
  if (!rootSnapshot.alive) {
    reject("TOP_DOCUMENT_CHANGED", "Top-level document is not available");
  }

  let parentDocument = topDocument;
  let parentSnapshot = rootSnapshot;
  const layers: FrameLayerBinding[] = [];

  request.framePath.forEach((name, index) => {
    const iframeElement = host.findFrameByName(parentDocument, name);
    if (iframeElement === null) {
      reject("FRAME_NOT_FOUND", `Frame "${name}" was not found`, index);
    }
    const elementSnapshot = host.snapshotFrameElement(iframeElement);
    if (!elementSnapshot.alive || elementSnapshot.frame === null) {
      reject("FRAME_ELEMENT_DETACHED", `Frame "${name}" is not live`, index);
    }
    if (!elementSnapshot.rendered) {
      reject("FRAME_NOT_RENDERED", `Frame "${name}" is not rendered`, index);
    }
    if (!idsEqual(elementSnapshot.ownerDocument, parentDocument)) {
      reject("FRAME_ELEMENT_REPLACED", `Frame "${name}" belongs to another document`, index);
    }

    const childDocument = elementSnapshot.frame.document;
    const childSnapshot = host.snapshotDocument(childDocument);
    if (!childSnapshot.alive) {
      reject("FRAME_DOCUMENT_CHANGED", `Frame "${name}" document is not live`, index);
    }

    const binding: FrameLayerBinding = {
      index,
      name,
      parentDocument,
      parentDocumentGeneration: parentSnapshot.generation,
      iframeElement,
      iframeElementId: elementSnapshot.elementId,
      frame: elementSnapshot.frame,
      frameId: elementSnapshot.frame.frameId,
      childDocument,
      childDocumentGeneration: childSnapshot.generation,
    };
    layers.push(binding);
    parentDocument = childDocument;
    parentSnapshot = childSnapshot;
  });

  const element = host.findElementById(parentDocument, request.targetElementId);
  if (element === null) {
    reject("TARGET_NOT_FOUND", `Target element "${request.targetElementId}" was not found`);
  }
  const targetSnapshot = host.snapshotElement(element);
  if (!targetSnapshot.alive) {
    reject("TARGET_ELEMENT_REMOVED", "Target element is not live");
  }
  const defaultAnchor =
    request.anchor ??
    {
      x: targetSnapshot.localBounds.x + targetSnapshot.localBounds.width / 2,
      y: targetSnapshot.localBounds.y + targetSnapshot.localBounds.height / 2,
    };

  const plan: OperationPlan = {
    version: 1,
    topDocument,
    topDocumentGeneration: rootSnapshot.generation,
    layers,
    target: {
      element,
      elementId: targetSnapshot.elementId,
      document: parentDocument,
      documentGeneration: parentSnapshot.generation,
      anchor: defaultAnchor,
    },
  };

  const resolution = resolvePlan(host, plan);
  return { plan, initialClickPoint: resolution.point };
}

export function executeOperationPlan(
  host: AutomationHost,
  plan: OperationPlan,
): ExecutionReceipt {
  const preflight = resolvePlan(host, plan);
  const innermostFrame = plan.layers.length === 0 ? null : plan.layers[plan.layers.length - 1].frame;

  const guard: ExecutionGuard = {
    assertUnchanged(): void {
      let current: PlanResolution;
      try {
        current = resolvePlan(host, plan);
      } catch (error) {
        reject(
          "EXECUTION_RACE_DETECTED",
          "Operation state changed between preflight and click",
          undefined,
          error,
        );
      }
      if (current.fingerprint !== preflight.fingerprint) {
        reject(
          "EXECUTION_RACE_DETECTED",
          "Operation geometry or layer identity changed between preflight and click",
        );
      }
    },
  };

  host.dispatchClick({
    target: plan.target.element,
    frame: innermostFrame,
    point: preflight.point,
    guard,
  });

  return {
    plan,
    targetId: plan.target.elementId,
    frameId: innermostFrame?.frameId ?? null,
    clickedAt: preflight.point,
  };
}
