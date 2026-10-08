import { parse, type Diagnostic, type ParseResult } from './parser';
import type { BlockNode, Container, LeafNode, RootNode } from './cst';
import { offsetToLineCol } from './verify';

export interface Edit {
  start: number;
  end: number;
  text: string;
}

export type EditResult =
  | { ok: true; source: string; edit: Edit }
  | { ok: false; diagnostics: Diagnostic[] };

export function applyEdit(source: string, edit: Edit): string {
  return source.slice(0, edit.start) + edit.text + source.slice(edit.end);
}

/** Map a candidate-source offset back to the original source across one edit. */
function mapOffsetToOriginal(candidate: number, edit: Edit): number {
  if (candidate <= edit.start) return candidate;
  const insertedEnd = edit.start + edit.text.length;
  if (candidate >= insertedEnd) return candidate - edit.text.length + (edit.end - edit.start);
  return edit.start; // inside the replaced/inserted text: point at the edit site
}

function diagnosticToOriginal(diag: Diagnostic, original: string, edit: Edit): Diagnostic {
  const start = mapOffsetToOriginal(diag.start, edit);
  const end = Math.max(start, mapOffsetToOriginal(diag.end, edit));
  const { line, col } = offsetToLineCol(original, start);
  return { ...diag, line, col, start, end };
}

/**
 * A document supports atomic, validated edits. Every edit is applied to a
 * candidate copy and reparsed; if the reparse produces more errors than the
 * original had, the edit is rejected and the document is left untouched.
 * Rejection diagnostics are reported in original-file coordinates.
 */
export class Document {
  constructor(public source: string) {}

  parse(): ParseResult {
    return parse(this.source);
  }

  private commit(edit: Edit): EditResult {
    const baseErrors = parse(this.source).errors.length;
    const candidate = applyEdit(this.source, edit);
    const errors = parse(candidate).errors;
    if (errors.length > baseErrors) {
      return {
        ok: false,
        diagnostics: errors.map((d) => diagnosticToOriginal(d, this.source, edit)),
      };
    }
    this.source = candidate;
    return { ok: true, source: candidate, edit };
  }

  /** Reprint only the value range of a leaf; every other byte is preserved. */
  setValue(leaf: LeafNode, value: string): EditResult {
    if (value === '' || /[\r\n]/.test(value)) {
      const { line, col } = offsetToLineCol(this.source, leaf.valueStart);
      return {
        ok: false,
        diagnostics: [
          {
            code: 'bad-line',
            message: 'value must be non-empty and single-line',
            line,
            col,
            start: leaf.valueStart,
            end: leaf.valueEnd,
          },
        ],
      };
    }
    return this.commit({ start: leaf.valueStart, end: leaf.valueEnd, text: value });
  }

  /** Insert whole lines (`text` must carry its own indentation) into a
   *  container at child position `index`. */
  insertBlock(parent: Container, index: number, text: string): EditResult {
    let offset: number;
    if (index < parent.children.length) {
      offset = parent.children[index].start;
    } else if (parent.kind === 'block') {
      offset =
        parent.children.length > 0
          ? parent.children[parent.children.length - 1].end
          : parent.headerEnd;
    } else {
      offset = parent.children.length > 0 ? parent.children[parent.children.length - 1].end : 0;
    }
    let insertion = text.endsWith('\n') ? text : text + '\n';
    if (offset > 0 && this.source[offset - 1] !== '\n') insertion = '\n' + insertion;
    return this.commit({ start: offset, end: offset, text: insertion });
  }

  /** Remove a node and its exact byte range (a block takes its whole subtree). */
  removeNode(node: LeafNode | BlockNode): EditResult {
    return this.commit({ start: node.start, end: node.end, text: '' });
  }
}
