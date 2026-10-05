/**
 * Incremental document: keeps the line table, lexed lines, node cache and
 * the current tree version, and applies line-based edits.
 *
 * Per edit:
 *  1. Re-lex only the touched lines, continuing forward only while the
 *     bracket-depth carry state keeps changing (stops at the first retained
 *     line whose lexed form is unchanged).
 *  2. Rebuild the block tree through the shared hash-consing cache, so every
 *     unchanged subtree is the identical object as in the previous version.
 *  3. Report the affected line range plus reuse statistics.
 */

import { lexLine, lexedEquals, type LexedLine } from './lexer.ts';
import {
  buildTree,
  type BuiltTree,
  type DocumentError,
  type NodeCache,
  type Occurrence,
  type SyntaxNode,
} from './parser.ts';

export interface LineRange {
  startLine: number;
  endLine: number;
}

export interface UpdateReport {
  /** Lines whose syntax actually changed (new-document coordinates). */
  affected: LineRange;
  /** Lines that had to be re-lexed (new-document coordinates). */
  relexed: LineRange;
  /** Node occurrences reused from the previous version (same object). */
  reusedNodes: number;
  /** Total node occurrences in the new version. */
  totalNodes: number;
  errors: DocumentError[];
}

export class IncrementalDocument {
  private lines: string[];
  private lexed: LexedLine[];
  private readonly cache: NodeCache = new Map();

  root!: SyntaxNode;
  occurrences!: Occurrence[];
  errors!: DocumentError[];
  version = 0;

  constructor(text: string) {
    this.lines = text.split('\n');
    this.lexed = [];
    let depth = 0;
    for (const line of this.lines) {
      const lexed = lexLine(line, depth);
      this.lexed.push(lexed);
      depth = lexed.depthOut;
    }
    this.reparse();
  }

  get text(): string {
    return this.lines.join('\n');
  }

  private reparse(): void {
    const built: BuiltTree = buildTree(this.lexed, this.cache);
    this.root = built.root;
    this.occurrences = built.occurrences;
    this.errors = built.errors;
  }

  /**
   * Replace `deleteCount` lines starting at `startLine` with `inserted`.
   */
  applyEdit(startLine: number, deleteCount: number, inserted: string[]): UpdateReport {
    const oldRoot = this.root;
    const oldOccurrences = this.occurrences;
    const delta = inserted.length - deleteCount;
    const editEndOld = startLine + deleteCount; // exclusive, old coordinates

    this.lines.splice(startLine, deleteCount, ...inserted);
    this.lexed.splice(startLine, deleteCount, ...new Array<LexedLine>(inserted.length).fill(null as unknown as LexedLine));

    // 1. Incremental re-lex: stop as soon as an old line re-lexes identically.
    let depth = startLine > 0 ? this.lexed[startLine - 1].depthOut : 0;
    let relexEnd = startLine - 1;
    for (let i = startLine; i < this.lines.length; i += 1) {
      const retained = i >= startLine + inserted.length ? this.lexed[i] : null;
      const next = lexLine(this.lines[i], depth);
      if (retained && lexedEquals(retained, next)) break;
      this.lexed[i] = next;
      depth = next.depthOut;
      relexEnd = i;
    }

    // 2. Rebuild the tree; unchanged subtrees intern to the same objects.
    const oldNodes = new Set<SyntaxNode>();
    const collect = (node: SyntaxNode): void => {
      oldNodes.add(node);
      node.children.forEach(collect);
    };
    collect(oldRoot);
    this.reparse();
    this.version += 1;

    let totalNodes = 0;
    let reusedNodes = 0;
    const count = (node: SyntaxNode): void => {
      totalNodes += 1;
      if (oldNodes.has(node)) reusedNodes += 1;
      node.children.forEach(count);
    };
    count(this.root);

    // 3. Affected range: occurrences that are not "same node, same (shifted)
    //    position" as before, plus the edited region itself.
    const oldPositions = new Map<SyntaxNode, Set<number>>();
    for (const occ of oldOccurrences) {
      if (occ.startLine >= startLine && occ.startLine < editEndOld) continue; // deleted
      const shifted = occ.startLine >= editEndOld ? occ.startLine + delta : occ.startLine;
      let starts = oldPositions.get(occ.node);
      if (!starts) {
        starts = new Set();
        oldPositions.set(occ.node, starts);
      }
      starts.add(shifted);
    }
    let minLine = Number.POSITIVE_INFINITY;
    let maxLine = Number.NEGATIVE_INFINITY;
    const touch = (from: number, to: number): void => {
      minLine = Math.min(minLine, from);
      maxLine = Math.max(maxLine, to);
    };
    for (const occ of this.occurrences) {
      const starts = oldPositions.get(occ.node);
      if (!starts || !starts.has(occ.startLine)) {
        touch(occ.startLine, occ.endLine);
      }
    }
    touch(startLine, Math.max(startLine, startLine + inserted.length - 1));

    return {
      affected: { startLine: minLine, endLine: maxLine },
      relexed: { startLine, endLine: relexEnd },
      reusedNodes,
      totalNodes,
      errors: this.errors,
    };
  }
}
