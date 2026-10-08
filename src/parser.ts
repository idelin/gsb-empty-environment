import type {
  Definition,
  Diagnostic,
  Heading,
  ParseResult,
  Range,
  Reference,
} from './types.ts';

const HEADING_RE = /^(#{1,6})[ \t]+(.*?)[ \t]*$/;
const DEFINITION_RE = /^\[\^([^\]\n]+)\][ \t]*:[ \t]*(.*)$/;
const REFERENCE_RE = /\[\^([^\]\n]+)\]/g;
const CONTINUATION_RE = /^(?:\t| {2,})/;

/** Normalize a reference/definition name for matching (Markdown-like). */
export function normalizeName(name: string): string {
  return name.trim().replace(/\s+/g, ' ').toLowerCase();
}

export interface RawBlock {
  startLine: number;
  lines: string[];
  text: string;
}

/** Parse of a single block; positions are relative to the block start. */
export interface BlockParse {
  headings: Heading[];
  references: Reference[];
  definitions: Definition[];
}

/** Split lines into blocks separated by blank lines. */
export function splitBlocks(lines: string[]): RawBlock[] {
  const blocks: RawBlock[] = [];
  let start = -1;
  for (let i = 0; i <= lines.length; i++) {
    const blank = i === lines.length || lines[i].trim() === '';
    if (!blank && start === -1) start = i;
    if (blank && start !== -1) {
      const blockLines = lines.slice(start, i);
      blocks.push({ startLine: start, lines: blockLines, text: blockLines.join('\n') });
      start = -1;
    }
  }
  return blocks;
}

function extractRefs(
  text: string,
  line: number,
  columnOffset: number,
  owner: Reference[] | null,
  sink: Reference[],
): void {
  for (const match of text.matchAll(REFERENCE_RE)) {
    const ref: Reference = {
      name: match[1],
      range: {
        start: { line, character: columnOffset + match.index! },
        end: { line, character: columnOffset + match.index! + match[0].length },
      },
      resolved: false,
    };
    sink.push(ref);
    owner?.push(ref);
  }
}

/** Parse one block of consecutive non-blank lines (relative positions). */
export function parseBlockLines(lines: string[]): BlockParse {
  const headings: Heading[] = [];
  const references: Reference[] = [];
  const definitions: Definition[] = [];

  let currentDef: Definition | null = null;
  let contentLines: string[] = [];
  let lastContentLine = -1;

  const finishDefinition = (): void => {
    if (!currentDef) return;
    currentDef.content = contentLines.join('\n');
    currentDef.range = {
      start: currentDef.range.start,
      end: { line: lastContentLine, character: lines[lastContentLine].length },
    };
    currentDef = null;
    contentLines = [];
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const defMatch = line.match(DEFINITION_RE);
    if (defMatch) {
      finishDefinition();
      const name = defMatch[1];
      const content = defMatch[2];
      const def: Definition = {
        name,
        content: '',
        range: {
          start: { line: i, character: 0 },
          end: { line: i, character: line.length },
        },
        nameRange: {
          start: { line: i, character: 0 },
          end: { line: i, character: name.length + 3 },
        },
        references: [],
      };
      definitions.push(def);
      currentDef = def;
      contentLines = [content];
      lastContentLine = i;
      extractRefs(content, i, line.length - content.length, def.references, references);
      continue;
    }
    if (currentDef && line.trim() !== '' && CONTINUATION_RE.test(line)) {
      const dedented = line.replace(CONTINUATION_RE, '');
      contentLines.push(dedented);
      lastContentLine = i;
      extractRefs(dedented, i, line.length - dedented.length, currentDef.references, references);
      continue;
    }
    finishDefinition();
    const headingMatch = line.match(HEADING_RE);
    if (headingMatch) {
      headings.push({
        level: headingMatch[1].length,
        text: headingMatch[2],
        range: {
          start: { line: i, character: 0 },
          end: { line: i, character: line.length },
        },
      });
    }
    extractRefs(line, i, 0, null, references);
  }
  finishDefinition();
  return { headings, references, definitions };
}

function shiftRange(range: Range, lineDelta: number): Range {
  return {
    start: { line: range.start.line + lineDelta, character: range.start.character },
    end: { line: range.end.line + lineDelta, character: range.end.character },
  };
}

/**
 * Copy a block parse, shifting all positions by `lineDelta`.
 * Always copies so document-level analysis never mutates cached parses.
 * References inside definitions keep object identity with the block's
 * top-level reference list.
 */
export function shiftBlockParse(block: BlockParse, lineDelta: number): BlockParse {
  const shiftedRefs = new Map<Reference, Reference>();
  for (const ref of block.references) {
    shiftedRefs.set(ref, { name: ref.name, range: shiftRange(ref.range, lineDelta), resolved: false });
  }
  return {
    headings: block.headings.map((h) => ({ ...h, range: shiftRange(h.range, lineDelta) })),
    references: block.references.map((r) => shiftedRefs.get(r)!),
    definitions: block.definitions.map((d) => ({
      name: d.name,
      content: d.content,
      range: shiftRange(d.range, lineDelta),
      nameRange: shiftRange(d.nameRange, lineDelta),
      references: d.references.map((r) => shiftedRefs.get(r)!),
    })),
  };
}

function compareDiagnostics(a: Diagnostic, b: Diagnostic): number {
  return (
    a.range.start.line - b.range.start.line ||
    a.range.start.character - b.range.start.character ||
    a.code.localeCompare(b.code)
  );
}

/**
 * Document-level analysis over assembled block parses: builds the symbol
 * table (first definition wins), resolves references, and emits
 * duplicate / undefined / circular diagnostics.
 */
export function analyzeDocument(parts: BlockParse[]): ParseResult {
  const headings: Heading[] = [];
  const references: Reference[] = [];
  const definitions: Definition[] = [];
  for (const part of parts) {
    headings.push(...part.headings);
    references.push(...part.references);
    definitions.push(...part.definitions);
  }

  const diagnostics: Diagnostic[] = [];
  const table = new Map<string, Definition>();
  for (const def of definitions) {
    const key = normalizeName(def.name);
    if (table.has(key)) {
      diagnostics.push({
        code: 'duplicate-definition',
        name: def.name,
        message: `Duplicate definition '${def.name}'; the first definition is kept.`,
        range: def.nameRange,
      });
    } else {
      table.set(key, def);
    }
  }

  for (const ref of references) {
    const target = table.get(normalizeName(ref.name));
    if (target) {
      ref.resolved = true;
      ref.target = target;
    } else {
      diagnostics.push({
        code: 'undefined-reference',
        name: ref.name,
        message: `Undefined footnote reference '${ref.name}'.`,
        range: ref.range,
      });
    }
  }

  diagnostics.push(...detectCycles([...table.values()]));
  diagnostics.sort(compareDiagnostics);
  return { headings, references, definitions, diagnostics };
}

/** Find definition cycles (A's content references B, B references A, ...). */
function detectCycles(defs: Definition[]): Diagnostic[] {
  const indexOf = new Map<Definition, number>(defs.map((d, i) => [d, i]));
  const edges: number[][] = defs.map((def) => {
    const out: number[] = [];
    for (const ref of def.references) {
      if (!ref.target) continue;
      const idx = indexOf.get(ref.target);
      if (idx !== undefined && !out.includes(idx)) out.push(idx);
    }
    return out;
  });

  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Array<number>(defs.length).fill(WHITE);
  const stack: number[] = [];
  const stackPos = new Map<number, number>();
  const reported = new Set<number>();
  const diagnostics: Diagnostic[] = [];

  for (let seed = 0; seed < defs.length; seed++) {
    if (color[seed] !== WHITE) continue;
    color[seed] = GRAY;
    stackPos.set(seed, stack.length);
    stack.push(seed);
    const frames = [{ node: seed, next: 0 }];
    while (frames.length > 0) {
      const frame = frames[frames.length - 1];
      if (frame.next < edges[frame.node].length) {
        const target = edges[frame.node][frame.next++];
        if (color[target] === WHITE) {
          color[target] = GRAY;
          stackPos.set(target, stack.length);
          stack.push(target);
          frames.push({ node: target, next: 0 });
        } else if (color[target] === GRAY) {
          const cycleNodes = stack.slice(stackPos.get(target)!);
          const path = [...cycleNodes, target].map((i) => `'${defs[i].name}'`).join(' -> ');
          for (const node of cycleNodes) {
            if (reported.has(node)) continue;
            reported.add(node);
            diagnostics.push({
              code: 'circular-footnote',
              name: defs[node].name,
              message: `Circular footnote definition: ${path}.`,
              range: defs[node].nameRange,
            });
          }
        }
      } else {
        frames.pop();
        color[frame.node] = BLACK;
        stack.pop();
        stackPos.delete(frame.node);
      }
    }
  }
  return diagnostics;
}

/** Full parse of a document (LF line endings). */
export function parseDocument(text: string): ParseResult {
  const lines = text.split('\n');
  const parts = splitBlocks(lines).map((block) =>
    shiftBlockParse(parseBlockLines(block.lines), block.startLine),
  );
  return analyzeDocument(parts);
}
