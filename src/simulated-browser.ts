import {
  type Affine,
  type Edges,
  type Point,
  type Rect,
  type Size,
  IDENTITY,
  ZERO_EDGES,
} from "./geometry.ts";
import {
  type AutomationHost,
  type ClickRequest,
  type DocumentHandle,
  type DocumentSnapshot,
  type ElementHandle,
  type ElementSnapshot,
  type FrameElementHandle,
  type FrameElementSnapshot,
  type FrameHandle,
  type FramePlacement,
  elementLocalToDocument,
} from "./host.ts";

let nextIdentity = 1;

function newId(prefix: string): string {
  const value = nextIdentity;
  nextIdentity += 1;
  return `${prefix}-${value}`;
}

export interface SimulatedDocument extends DocumentHandle {
  readonly documentId: string;
  generation: number;
  alive: boolean;
  viewport: Size;
  scrollX: number;
  scrollY: number;
  readonly frames: SimulatedIframeElement[];
  readonly elements: SimulatedTargetElement[];
}

export interface SimulatedFrame extends FrameHandle {
  readonly frameId: string;
  document: SimulatedDocument;
  alive: boolean;
}

export interface SimulatedIframeElement extends FrameElementHandle {
  readonly elementId: string;
  readonly name: string;
  readonly ownerDocument: SimulatedDocument;
  readonly frame: SimulatedFrame;
  placement: FramePlacement;
  rendered: boolean;
  mounted: boolean;
  clipInParentDocument: Rect | null;
}

export interface SimulatedTargetElement extends ElementHandle {
  readonly elementId: string;
  readonly ownerDocument: SimulatedDocument;
  rect: Rect;
  transform: Affine;
  transformOrigin: Point;
  clipInDocument: Rect | null;
  rendered: boolean;
  alive: boolean;
}

export interface FrameOptions {
  readonly name: string;
  readonly rect: Rect;
  readonly viewport?: Size;
  readonly border?: Edges;
  readonly padding?: Edges;
  readonly transform?: Affine;
  readonly transformOrigin?: Point;
  readonly clipInParentDocument?: Rect | null;
  readonly rendered?: boolean;
}

export interface TargetOptions {
  readonly elementId?: string;
  readonly rect: Rect;
  readonly transform?: Affine;
  readonly transformOrigin?: Point;
  readonly clipInDocument?: Rect | null;
  readonly rendered?: boolean;
}

export interface ClickRecord {
  readonly sequence: number;
  readonly targetId: string;
  readonly frameId: string | null;
  readonly point: Point;
}

export class SimulatedBrowser implements AutomationHost {
  root: SimulatedDocument;
  readonly clicks: ClickRecord[] = [];
  onBeforeDispatch: (() => void) | null = null;

  constructor(viewport: Size) {
    this.root = this.createDocument(viewport, 1);
  }

  rootDocument(): DocumentHandle {
    return this.root;
  }

  createDocument(viewport: Size, generation: number): SimulatedDocument {
    return {
      documentId: newId("document"),
      generation,
      alive: true,
      viewport: { ...viewport },
      scrollX: 0,
      scrollY: 0,
      frames: [],
      elements: [],
    };
  }

  createFrame(parent: SimulatedDocument, options: FrameOptions): SimulatedIframeElement {
    if (!parent.alive) {
      throw new Error("Cannot create a frame in a dead document");
    }

    const border = options.border ?? ZERO_EDGES;
    const padding = options.padding ?? ZERO_EDGES;
    const viewport =
      options.viewport ?? {
        width:
          options.rect.width - border.left - border.right - padding.left - padding.right,
        height:
          options.rect.height - border.top - border.bottom - padding.top - padding.bottom,
      };
    if (viewport.width < 0 || viewport.height < 0) {
      throw new Error("Frame content viewport cannot be negative");
    }

    const frameDocument = this.createDocument(viewport, 1);
    const frame: SimulatedFrame = {
      frameId: newId("frame"),
      document: frameDocument,
      alive: true,
    };
    const iframe: SimulatedIframeElement = {
      elementId: newId("iframe"),
      name: options.name,
      ownerDocument: parent,
      frame,
      placement: {
        rect: options.rect,
        border,
        padding,
        transform: options.transform ?? IDENTITY,
        transformOrigin: options.transformOrigin ?? { x: 0, y: 0 },
      },
      rendered: options.rendered ?? true,
      mounted: true,
      clipInParentDocument: options.clipInParentDocument ?? null,
    };
    parent.frames.push(iframe);
    return iframe;
  }

  createTarget(document: SimulatedDocument, options: TargetOptions): SimulatedTargetElement {
    if (!document.alive) {
      throw new Error("Cannot create a target in a dead document");
    }
    const target: SimulatedTargetElement = {
      elementId: options.elementId ?? newId("element"),
      ownerDocument: document,
      rect: options.rect,
      transform: options.transform ?? IDENTITY,
      transformOrigin: options.transformOrigin ?? { x: 0, y: 0 },
      clipInDocument: options.clipInDocument ?? null,
      rendered: options.rendered ?? true,
      alive: true,
    };
    document.elements.push(target);
    return target;
  }

  scrollTo(document: SimulatedDocument, x: number, y: number): void {
    if (!document.alive) {
      throw new Error("Cannot scroll a dead document");
    }
    document.scrollX = x;
    document.scrollY = y;
  }

  navigateTopLevel(viewport: Size = this.root.viewport): SimulatedDocument {
    const previous = this.root;
    const nextGeneration = previous.generation + 1;
    retireDocumentTree(previous);
    this.root = this.createDocument(viewport, nextGeneration);
    return this.root;
  }

  navigateFrame(frame: SimulatedFrame, viewport: Size = frame.document.viewport): SimulatedDocument {
    if (!frame.alive) {
      throw new Error("Cannot navigate a dead frame");
    }
    const previous = frame.document;
    const nextGeneration = previous.generation + 1;
    retireDocumentTree(previous);
    const next = this.createDocument(viewport, nextGeneration);
    frame.document = next;
    return next;
  }

  /**
   * Replaces the iframe element with a fresh element that intentionally keeps
   * the same name. Plans must reject this instead of re-resolving by name.
   */
  replaceFrame(
    parent: SimulatedDocument,
    name: string,
    overrides: Partial<FrameOptions> = {},
  ): SimulatedIframeElement {
    const index = parent.frames.findIndex(
      (candidate) => candidate.mounted && candidate.name === name,
    );
    if (index < 0) {
      throw new Error(`Frame "${name}" was not found`);
    }
    const previous = parent.frames[index];
    if (!previous.ownerDocument.alive) {
      throw new Error("Cannot replace a frame in a dead document");
    }
    previous.mounted = false;
    previous.frame.alive = false;
    retireDocumentTree(previous.frame.document);
    parent.frames.splice(index, 1);

    return this.createFrame(parent, {
      name,
      rect: overrides.rect ?? previous.placement.rect,
      viewport: overrides.viewport ?? previous.frame.document.viewport,
      border: overrides.border ?? previous.placement.border,
      padding: overrides.padding ?? previous.placement.padding,
      transform: overrides.transform ?? previous.placement.transform,
      transformOrigin: overrides.transformOrigin ?? previous.placement.transformOrigin,
      clipInParentDocument:
        overrides.clipInParentDocument === undefined
          ? previous.clipInParentDocument
          : overrides.clipInParentDocument,
      rendered: overrides.rendered ?? previous.rendered,
    });
  }

  removeTarget(target: SimulatedTargetElement): void {
    target.alive = false;
    target.rendered = false;
    const document = target.ownerDocument;
    const index = document.elements.indexOf(target);
    if (index >= 0) {
      document.elements.splice(index, 1);
    }
  }

  snapshotDocument(handle: DocumentHandle): DocumentSnapshot {
    const document = handle as SimulatedDocument;
    if (!isSimulatedDocument(document)) {
      return {
        alive: false,
        generation: -1,
        viewport: { width: 0, height: 0 },
        scrollX: 0,
        scrollY: 0,
      };
    }
    return {
      alive: document.alive,
      generation: document.generation,
      viewport: { ...document.viewport },
      scrollX: document.scrollX,
      scrollY: document.scrollY,
    };
  }

  findFrameByName(parent: DocumentHandle, name: string): FrameElementHandle | null {
    const document = parent as SimulatedDocument;
    if (!isSimulatedDocument(document) || !document.alive) {
      return null;
    }
    const iframe = document.frames.find(
      (candidate) => candidate.mounted && candidate.name === name,
    );
    return iframe ?? null;
  }

  snapshotFrameElement(handle: FrameElementHandle): FrameElementSnapshot {
    const iframe = handle as SimulatedIframeElement;
    if (!isSimulatedIframeElement(iframe)) {
      throw new Error("Unknown simulated iframe handle");
    }
    return {
      alive: iframe.mounted && iframe.ownerDocument.alive && iframe.frame.alive,
      elementId: iframe.elementId,
      ownerDocument: iframe.ownerDocument,
      frame: iframe.frame,
      placement: {
        rect: iframe.placement.rect,
        border: iframe.placement.border,
        padding: iframe.placement.padding,
        transform: iframe.placement.transform,
        transformOrigin: iframe.placement.transformOrigin,
      },
      rendered: iframe.rendered,
      clipInParentDocument: iframe.clipInParentDocument,
    };
  }

  findElementById(document: DocumentHandle, elementId: string): ElementHandle | null {
    const doc = document as SimulatedDocument;
    if (!isSimulatedDocument(doc) || !doc.alive) {
      return null;
    }
    const element = doc.elements.find(
      (candidate) => candidate.alive && candidate.elementId === elementId,
    );
    return element ?? null;
  }

  snapshotElement(handle: ElementHandle): ElementSnapshot {
    const element = handle as SimulatedTargetElement;
    if (!isSimulatedTargetElement(element)) {
      throw new Error("Unknown simulated element handle");
    }
    return {
      alive: element.alive && element.ownerDocument.alive,
      elementId: element.elementId,
      ownerDocument: element.ownerDocument,
      rendered: element.rendered,
      localBounds: { x: 0, y: 0, width: element.rect.width, height: element.rect.height },
      localToDocument: elementLocalToDocument(
        element.rect,
        element.transform,
        element.transformOrigin,
      ),
      clipInDocument: element.clipInDocument,
    };
  }

  dispatchClick(request: ClickRequest): void {
    this.onBeforeDispatch?.();
    request.guard.assertUnchanged();
    this.clicks.push({
      sequence: this.clicks.length + 1,
      targetId: request.target.elementId,
      frameId: request.frame?.frameId ?? null,
      point: { ...request.point },
    });
  }
}

function retireDocumentTree(document: SimulatedDocument): void {
  if (!document.alive) {
    return;
  }
  document.alive = false;
  for (const element of document.elements) {
    element.alive = false;
  }
  for (const iframe of document.frames) {
    iframe.mounted = false;
    iframe.frame.alive = false;
    retireDocumentTree(iframe.frame.document);
  }
}

function isSimulatedDocument(value: unknown): value is SimulatedDocument {
  return (
    typeof value === "object" &&
    value !== null &&
    "documentId" in value &&
    "frames" in value &&
    "elements" in value
  );
}

function isSimulatedIframeElement(value: unknown): value is SimulatedIframeElement {
  return (
    typeof value === "object" &&
    value !== null &&
    "elementId" in value &&
    "placement" in value &&
    "frame" in value
  );
}

function isSimulatedTargetElement(value: unknown): value is SimulatedTargetElement {
  return (
    typeof value === "object" &&
    value !== null &&
    "elementId" in value &&
    "rect" in value &&
    "ownerDocument" in value
  );
}
