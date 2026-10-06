import { Canvas, type CanvasNode, type NodeId } from "./model.ts";
import { IdAllocator } from "./ids.ts";
import { paste, type Clipboard, type PasteResult } from "./clipboard.ts";

export interface Command {
  execute(): void;
  undo(): void;
}

/**
 * A paste as an undoable command. The ids allocated on the first execution
 * are cached, so undo/redo cycles never consume additional public ids.
 */
export class PasteCommand implements Command {
  private readonly canvas: Canvas;
  private readonly clipboard: Clipboard;
  private readonly allocator: IdAllocator;
  private pasted: CanvasNode[] | null = null;

  constructor(canvas: Canvas, clipboard: Clipboard, allocator: IdAllocator) {
    this.canvas = canvas;
    this.clipboard = clipboard;
    this.allocator = allocator;
  }

  /** The result of the first execution; undefined before it runs. */
  get result(): PasteResult | undefined {
    if (!this.pasted) {
      return undefined;
    }
    const idMap = new Map<NodeId, NodeId>();
    this.clipboard.nodes.forEach((node, index) => {
      idMap.set(node.id, this.pasted![index]!.id);
    });
    return { nodes: this.pasted, idMap };
  }

  execute(): void {
    if (this.pasted) {
      // Redo: re-insert the very same nodes; no new ids are drawn.
      for (const node of this.pasted) {
        this.canvas.addNode(node);
      }
      return;
    }
    this.pasted = paste(this.canvas, this.clipboard, this.allocator).nodes;
  }

  undo(): void {
    if (!this.pasted) {
      return;
    }
    for (const node of this.pasted) {
      this.canvas.removeNode(node.id);
    }
  }
}

export class History {
  private readonly undoStack: Command[] = [];
  private readonly redoStack: Command[] = [];

  do(command: Command): void {
    command.execute();
    this.undoStack.push(command);
    this.redoStack.length = 0;
  }

  undo(): void {
    const command = this.undoStack.pop();
    if (!command) {
      return;
    }
    command.undo();
    this.redoStack.push(command);
  }

  redo(): void {
    const command = this.redoStack.pop();
    if (!command) {
      return;
    }
    command.execute();
    this.undoStack.push(command);
  }
}
