import { History } from "./history.ts";

export interface DrawObject {
  id: string;
  x: number;
  y: number;
}

export interface DrawingDocument {
  objects: Record<string, DrawObject>;
}

export const LABEL_MOVE = "move";
export const LABEL_ADD = "add-object";
export const LABEL_REMOVE = "remove-object";

export class DrawingEditor {
  readonly history: History<DrawingDocument>;
  private doc: DrawingDocument;

  constructor(initial?: DrawingDocument, capacity?: number) {
    this.doc = initial ?? { objects: {} };
    this.history = new History<DrawingDocument>(this.doc, { capacity });
  }

  get snapshot(): DrawingDocument {
    return this.history.current;
  }

  moveObject(id: string, dx: number, dy: number): void {
    const target = this.doc.objects[id];
    if (target === undefined) {
      throw new Error(`unknown object: ${id}`);
    }
    this.doc = {
      objects: {
        ...this.doc.objects,
        [id]: { ...target, x: target.x + dx, y: target.y + dy },
      },
    };
    this.history.commit({ label: LABEL_MOVE, mergeKey: id, doc: this.doc });
  }

  addObject(object: DrawObject): void {
    this.doc = {
      objects: { ...this.doc.objects, [object.id]: { ...object } },
    };
    this.history.commit({ label: LABEL_ADD, mergeKey: object.id, doc: this.doc });
  }

  removeObject(id: string): void {
    const objects = { ...this.doc.objects };
    delete objects[id];
    this.doc = { objects };
    this.history.commit({ label: LABEL_REMOVE, mergeKey: id, doc: this.doc });
  }

  undo(): boolean {
    const doc = this.history.undo();
    if (doc === undefined) {
      return false;
    }
    this.doc = doc;
    return true;
  }

  redo(): boolean {
    const doc = this.history.redo();
    if (doc === undefined) {
      return false;
    }
    this.doc = doc;
    return true;
  }

  markSaved(): void {
    this.history.markSaved();
  }

  isDirty(): boolean {
    return this.history.isDirty();
  }
}
