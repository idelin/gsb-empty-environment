import {
  analyzeDocument,
  normalizeName,
  parseBlockLines,
  shiftBlockParse,
  splitBlocks,
} from './parser.ts';
import type { BlockParse } from './parser.ts';
import type {
  AffectedReference,
  Definition,
  Diagnostic,
  Heading,
  ParseResult,
  Position,
  Range,
  Reference,
  TextEdit,
  UpdateResult,
} from './types.ts';

interface Block {
  id: number;
  startLine: number;
  text: string;
  /** Cached parse with positions relative to the block start. */
  parsed: BlockParse;
}

/** Lightweight per-reference snapshot used to diff across edits. */
interface RefInfo {
  key: string;
  name: string;
  normName: string;
  range: Range;
  resolved: boolean;
  targetNormName: string | null;
  targetContent: string | null;
}

function lineStartsOf(text: string): number[] {
  const starts = [0];
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\n') starts.push(i + 1);
  }
  return starts;
}

function offsetAt(starts: number[], position: Position): number {
  if (position.line < 0 || position.line >= starts.length) {
    throw new RangeError(`Line ${position.line} is out of range.`);
  }
  return starts[position.line] + position.character;
}

function compareRanges(a: Range, b: Range): number {
  return a.start.line - b.start.line || a.start.character - b.start.character;
}

/**
 * A mutable document with incremental updates. Unchanged blocks keep their
 * cached parse; edits only re-parse the blocks they touch, while symbol
 * resolution and diagnostics are recomputed over the assembled state.
 */
export class Document {
  private text: string;
  private blocks: Block[] = [];
  private nextBlockId = 1;
  private state: ParseResult;
  private refInfos: Map<string, RefInfo> = new Map();

  private constructor(text: string) {
    this.text = text;
    this.state = this.rebuild();
  }

  static from(text: string): Document {
    return new Document(text);
  }

  getText(): string {
    return this.text;
  }

  getHeadings(): Heading[] {
    return this.state.headings;
  }

  getReferences(): Reference[] {
    return this.state.references;
  }

  getDefinitions(): Definition[] {
    return this.state.definitions;
  }

  getDiagnostics(): Diagnostic[] {
    return this.state.diagnostics;
  }

  /** Apply one edit; returns the affected references and new diagnostics. */
  applyEdit(edit: TextEdit): UpdateResult {
    const starts = lineStartsOf(this.text);
    const start = offsetAt(starts, edit.range.start);
    const end = offsetAt(starts, edit.range.end);
    if (start > end || end > this.text.length) {
      throw new RangeError('Edit range is out of bounds.');
    }
    const previousInfos = this.refInfos;
    this.text = this.text.slice(0, start) + edit.newText + this.text.slice(end);
    this.state = this.rebuild();
    return {
      affectedReferences: diffReferences(previousInfos, this.refInfos),
      diagnostics: this.state.diagnostics,
    };
  }

  /**
   * Re-split into blocks, reuse parses of blocks whose text is unchanged
   * (matched by common prefix/suffix), re-parse the rest, then re-run
   * document-level analysis.
   */
  private rebuild(): ParseResult {
    const raws = splitBlocks(this.text.split('\n'));
    const oldBlocks = this.blocks;

    let prefix = 0;
    while (
      prefix < raws.length &&
      prefix < oldBlocks.length &&
      raws[prefix].text === oldBlocks[prefix].text
    ) {
      prefix++;
    }
    let suffix = 0;
    while (
      suffix < raws.length - prefix &&
      suffix < oldBlocks.length - prefix &&
      raws[raws.length - 1 - suffix].text === oldBlocks[oldBlocks.length - 1 - suffix].text
    ) {
      suffix++;
    }

    const blocks: Block[] = [];
    for (let i = 0; i < prefix; i++) {
      blocks.push({ ...oldBlocks[i], startLine: raws[i].startLine });
    }
    for (let i = prefix; i < raws.length - suffix; i++) {
      blocks.push({
        id: this.nextBlockId++,
        startLine: raws[i].startLine,
        text: raws[i].text,
        parsed: parseBlockLines(raws[i].lines),
      });
    }
    for (let i = 0; i < suffix; i++) {
      const old = oldBlocks[oldBlocks.length - suffix + i];
      blocks.push({ ...old, startLine: raws[raws.length - suffix + i].startLine });
    }
    this.blocks = blocks;

    const parts = blocks.map((block) => shiftBlockParse(block.parsed, block.startLine));
    const state = analyzeDocument(parts);

    const infos = new Map<string, RefInfo>();
    let refCursor = 0;
    for (let i = 0; i < blocks.length; i++) {
      for (let j = 0; j < parts[i].references.length; j++) {
        const ref = state.references[refCursor++];
        infos.set(`${blocks[i].id}:${j}`, {
          key: `${blocks[i].id}:${j}`,
          name: ref.name,
          normName: normalizeName(ref.name),
          range: ref.range,
          resolved: ref.resolved,
          targetNormName: ref.target ? normalizeName(ref.target.name) : null,
          targetContent: ref.target ? ref.target.content : null,
        });
      }
    }
    this.refInfos = infos;
    return state;
  }
}

function diffReferences(
  oldInfos: Map<string, RefInfo>,
  newInfos: Map<string, RefInfo>,
): AffectedReference[] {
  const affected: AffectedReference[] = [];
  const matched: Array<[RefInfo, RefInfo]> = [];
  const oldUnmatched = new Map(oldInfos);
  const newUnmatched = new Map(newInfos);

  for (const [key, info] of newInfos) {
    const old = oldInfos.get(key);
    if (old) {
      matched.push([old, info]);
      oldUnmatched.delete(key);
      newUnmatched.delete(key);
    }
  }

  // Blocks that were re-parsed get fresh keys; pair their references with
  // leftover old references by normalized name, in document order.
  const oldByName = new Map<string, RefInfo[]>();
  for (const info of oldUnmatched.values()) {
    const queue = oldByName.get(info.normName) ?? [];
    queue.push(info);
    oldByName.set(info.normName, queue);
  }
  for (const [key, info] of newUnmatched) {
    const queue = oldByName.get(info.normName);
    if (queue && queue.length > 0) {
      const old = queue.shift()!;
      matched.push([old, info]);
      oldUnmatched.delete(old.key);
      newUnmatched.delete(key);
    }
  }

  for (const [old, current] of matched) {
    if (old.resolved !== current.resolved) {
      affected.push({
        change: 'resolution-changed',
        name: current.name,
        range: current.range,
        previousRange: old.range,
      });
    } else if (
      current.resolved &&
      (old.targetNormName !== current.targetNormName ||
        old.targetContent !== current.targetContent)
    ) {
      affected.push({
        change: 'target-changed',
        name: current.name,
        range: current.range,
        previousRange: old.range,
      });
    }
  }
  for (const info of newUnmatched.values()) {
    affected.push({ change: 'added', name: info.name, range: info.range });
  }
  for (const info of oldUnmatched.values()) {
    affected.push({ change: 'removed', name: info.name, range: info.range });
  }
  affected.sort((a, b) => compareRanges(a.range, b.range));
  return affected;
}
