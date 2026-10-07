import { Graph, type CanvasNode, type NodeId } from './graph.ts';
import type { IdAllocator } from './id-allocator.ts';
import { pasteSubgraph, type PasteResult } from './paste.ts';

export interface Command {
  execute(): void;
  undo(): void;
}

/**
 * Undo/redo stack. `execute` runs a command and clears the redo stack,
 * matching standard editor semantics.
 */
export class History {
  private readonly undoStack: Command[] = [];
  private readonly redoStack: Command[] = [];

  execute(command: Command): void {
    command.execute();
    this.undoStack.push(command);
    this.redoStack.length = 0;
  }

  get canUndo(): boolean {
    return this.undoStack.length > 0;
  }

  get canRedo(): boolean {
    return this.redoStack.length > 0;
  }

  undo(): void {
    const command = this.undoStack.pop();
    if (command === undefined) throw new Error('nothing to undo');
    command.undo();
    this.redoStack.push(command);
  }

  redo(): void {
    const command = this.redoStack.pop();
    if (command === undefined) throw new Error('nothing to redo');
    command.execute();
    this.undoStack.push(command);
  }
}

/**
 * Paste as an undoable command.
 *
 * The paste itself is computed once (id allocation + rewiring); undo
 * removes exactly the inserted nodes and redo re-inserts the very same
 * nodes, so redo never consumes additional public ids and always
 * restores the identical state.
 */
export class PasteCommand implements Command {
  private result: PasteResult | undefined;
  private readonly clipboard: readonly CanvasNode[];
  private readonly graph: Graph;
  private readonly allocator: IdAllocator;

  constructor(
    clipboard: readonly CanvasNode[],
    graph: Graph,
    allocator: IdAllocator,
  ) {
    this.clipboard = clipboard;
    this.graph = graph;
    this.allocator = allocator;
  }

  execute(): PasteResult {
    if (this.result === undefined) {
      this.result = pasteSubgraph(this.clipboard, this.graph, this.allocator);
    } else {
      this.graph.addAll(this.result.nodes);
    }
    return this.result;
  }

  undo(): void {
    if (this.result === undefined) throw new Error('command not executed');
    for (const node of this.result.nodes) {
      this.graph.remove(node.id);
    }
  }

  get pastedIds(): readonly NodeId[] {
    return (this.result?.nodes ?? []).map((n) => n.id);
  }
}
