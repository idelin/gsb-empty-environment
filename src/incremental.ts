/**
 * Incremental JSON parser. Feed arbitrary string chunks via write(); the
 * parser may pause at any byte boundary. A number token is buffered (its raw
 * fragment kept across chunks) and only published as an event once a
 * delimiter or end-of-input proves the token complete -- half a number is
 * never emitted. parseJson() is defined as one-shot over the same machine,
 * so chunked and whole-input parsing agree by construction.
 */

import { parseDecimal } from './decimal.ts';
import type { Decimal, DecimalOptions } from './decimal.ts';

export class JsonSyntaxError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(offset >= 0 ? `${message} (offset ${offset})` : message);
    this.name = 'JsonSyntaxError';
    this.offset = offset;
  }
}

export interface JsonNumber {
  readonly kind: 'number';
  readonly decimal: Decimal;
}

export type JsonValue =
  | null
  | boolean
  | string
  | JsonNumber
  | JsonValue[]
  | { [key: string]: JsonValue };

export function isJsonNumber(v: JsonValue): v is JsonNumber {
  return typeof v === 'object' && v !== null && !Array.isArray(v) && (v as JsonNumber).kind === 'number';
}

export type JsonEvent =
  | { readonly type: 'startObject' }
  | { readonly type: 'endObject' }
  | { readonly type: 'startArray' }
  | { readonly type: 'endArray' }
  | { readonly type: 'key'; readonly value: string }
  | { readonly type: 'value'; readonly value: JsonValue };

type NumState = 'intStart' | 'intZero' | 'int' | 'fracStart' | 'frac' | 'expStart' | 'expSign' | 'exp';
type ObjectExpect = 'keyOrEnd' | 'key' | 'colon' | 'value' | 'commaOrEnd';
type ArrayExpect = 'valueOrEnd' | 'value' | 'commaOrEnd';
interface ObjectCtx { kind: 'object'; expect: ObjectExpect }
interface ArrayCtx { kind: 'array'; expect: ArrayExpect }
type Mode = 'default' | 'string' | 'escape' | 'unicode' | 'number' | 'literal';

const isDigit = (c: string): boolean => c >= '0' && c <= '9';
const isHex = (c: string): boolean =>
  (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');

export class IncrementalJsonParser {
  private readonly opts: DecimalOptions;
  private offset = 0;
  private mode: Mode = 'default';
  private readonly stack: (ObjectCtx | ArrayCtx)[] = [];
  private rootDone = false;
  private strBuf = '';
  private hexBuf = '';
  private numRaw = '';
  private numState: NumState = 'int';
  private litBuf = '';

  constructor(opts: DecimalOptions = {}) {
    this.opts = opts;
  }

  /** Consume a chunk; returns events completed by these characters. */
  write(chunk: string): JsonEvent[] {
    const events: JsonEvent[] = [];
    for (const ch of chunk) {
      this.feed(ch, events);
      this.offset++;
    }
    return events;
  }

  /** Signal end of input; returns any final events. Throws if incomplete. */
  end(): JsonEvent[] {
    const events: JsonEvent[] = [];
    if (this.mode === 'number') this.finishNumber(events);
    else if (this.mode === 'literal') this.finishLiteral(events);
    else if (this.mode !== 'default') {
      throw new JsonSyntaxError('unterminated string', this.offset);
    }
    if (this.stack.length !== 0) {
      throw new JsonSyntaxError(
        `unexpected end of input: unclosed ${this.stack[this.stack.length - 1].kind}`,
        this.offset,
      );
    }
    if (!this.rootDone) {
      throw new JsonSyntaxError('unexpected end of input: no JSON value', this.offset);
    }
    return events;
  }

  private feed(ch: string, events: JsonEvent[]): void {
    switch (this.mode) {
      case 'default': this.feedDefault(ch, events); return;
      case 'string': this.feedString(ch, events); return;
      case 'escape': this.feedEscape(ch); return;
      case 'unicode': this.feedUnicode(ch); return;
      case 'number': this.feedNumber(ch, events); return;
      case 'literal': this.feedLiteral(ch, events); return;
    }
  }

  private feedDefault(ch: string, events: JsonEvent[]): void {
    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') return;
    const top = this.stack[this.stack.length - 1];
    if (top === undefined) {
      if (this.rootDone) {
        throw new JsonSyntaxError(`unexpected character ${JSON.stringify(ch)} after root value`, this.offset);
      }
      this.feedValueStart(ch, events);
      return;
    }
    if (top.kind === 'object') {
      switch (top.expect) {
        case 'keyOrEnd':
          if (ch === '}') { this.stack.pop(); events.push({ type: 'endObject' }); this.valueCompleted(); return; }
          if (ch === '"') { this.mode = 'string'; this.strBuf = ''; return; }
          break;
        case 'key':
          if (ch === '"') { this.mode = 'string'; this.strBuf = ''; return; }
          break;
        case 'colon':
          if (ch === ':') { top.expect = 'value'; return; }
          break;
        case 'value':
          this.feedValueStart(ch, events); return;
        case 'commaOrEnd':
          if (ch === ',') { top.expect = 'key'; return; }
          if (ch === '}') { this.stack.pop(); events.push({ type: 'endObject' }); this.valueCompleted(); return; }
          break;
      }
      throw new JsonSyntaxError(`unexpected character ${JSON.stringify(ch)} in object`, this.offset);
    }
    switch (top.expect) {
      case 'valueOrEnd':
        if (ch === ']') { this.stack.pop(); events.push({ type: 'endArray' }); this.valueCompleted(); return; }
        this.feedValueStart(ch, events); return;
      case 'value':
        this.feedValueStart(ch, events); return;
      case 'commaOrEnd':
        if (ch === ',') { top.expect = 'value'; return; }
        if (ch === ']') { this.stack.pop(); events.push({ type: 'endArray' }); this.valueCompleted(); return; }
        break;
    }
    throw new JsonSyntaxError(`unexpected character ${JSON.stringify(ch)} in array`, this.offset);
  }

  private feedValueStart(ch: string, events: JsonEvent[]): void {
    if (ch === '"') { this.mode = 'string'; this.strBuf = ''; return; }
    if (ch === '{') { this.stack.push({ kind: 'object', expect: 'keyOrEnd' }); events.push({ type: 'startObject' }); return; }
    if (ch === '[') { this.stack.push({ kind: 'array', expect: 'valueOrEnd' }); events.push({ type: 'startArray' }); return; }
    if (ch === '-' || isDigit(ch)) {
      this.mode = 'number';
      this.numRaw = ch;
      this.numState = ch === '-' ? 'intStart' : ch === '0' ? 'intZero' : 'int';
      return;
    }
    if (ch === 't' || ch === 'f' || ch === 'n') { this.mode = 'literal'; this.litBuf = ch; return; }
    throw new JsonSyntaxError(`unexpected character ${JSON.stringify(ch)}`, this.offset);
  }

  private valueCompleted(): void {
    const top = this.stack[this.stack.length - 1];
    if (top === undefined) { this.rootDone = true; return; }
    top.expect = 'commaOrEnd';
  }

  private feedNumber(ch: string, events: JsonEvent[]): void {
    const digit = isDigit(ch);
    let next: NumState | null = null;
    switch (this.numState) {
      case 'intStart': if (digit) next = ch === '0' ? 'intZero' : 'int'; break;
      case 'intZero': if (ch === '.') next = 'fracStart'; else if (ch === 'e' || ch === 'E') next = 'expStart'; break;
      case 'int': if (digit) next = 'int'; else if (ch === '.') next = 'fracStart'; else if (ch === 'e' || ch === 'E') next = 'expStart'; break;
      case 'fracStart': if (digit) next = 'frac'; break;
      case 'frac': if (digit) next = 'frac'; else if (ch === 'e' || ch === 'E') next = 'expStart'; break;
      case 'expStart': if (digit) next = 'exp'; else if (ch === '+' || ch === '-') next = 'expSign'; break;
      case 'expSign': if (digit) next = 'exp'; break;
      case 'exp': if (digit) next = 'exp'; break;
    }
    if (next !== null) { this.numRaw += ch; this.numState = next; return; }
    if (digit || ch === '.' || ch === 'e' || ch === 'E' || ch === '+' || ch === '-') {
      // e.g. "01", "1..2", "1e+" then another sign: illegal inside a number.
      throw new JsonSyntaxError(`invalid character ${JSON.stringify(ch)} in number`, this.offset);
    }
    this.finishNumber(events);
    this.feedDefault(ch, events);
  }

  private finishNumber(events: JsonEvent[]): void {
    const st = this.numState;
    if (st === 'intStart' || st === 'fracStart' || st === 'expStart' || st === 'expSign') {
      throw new JsonSyntaxError(`incomplete number ${JSON.stringify(this.numRaw)}`, this.offset);
    }
    // Grammar already enforced by the state machine; parseDecimal applies the
    // precision/exponent budgets and may throw DecimalError.
    const decimal = parseDecimal(this.numRaw, this.opts);
    this.mode = 'default';
    events.push({ type: 'value', value: { kind: 'number', decimal } });
    this.valueCompleted();
  }

  private feedLiteral(ch: string, events: JsonEvent[]): void {
    if (ch >= 'a' && ch <= 'z') { this.litBuf += ch; return; }
    this.finishLiteral(events);
    this.feedDefault(ch, events);
  }

  private finishLiteral(events: JsonEvent[]): void {
    const v = this.litBuf === 'true' ? true : this.litBuf === 'false' ? false : this.litBuf === 'null' ? null : undefined;
    if (v === undefined) {
      throw new JsonSyntaxError(`invalid literal ${JSON.stringify(this.litBuf)}`, this.offset);
    }
    this.mode = 'default';
    events.push({ type: 'value', value: v });
    this.valueCompleted();
  }

  private feedString(ch: string, events: JsonEvent[]): void {
    if (ch === '"') { this.finishString(events); return; }
    if (ch === '\\') { this.mode = 'escape'; return; }
    if (ch < '') {
      throw new JsonSyntaxError('unescaped control character in string', this.offset);
    }
    this.strBuf += ch;
  }

  private feedEscape(ch: string): void {
    if (ch === 'u') { this.mode = 'unicode'; this.hexBuf = ''; return; }
    const rep =
      ch === '"' ? '"' : ch === '\\' ? '\\' : ch === '/' ? '/' :
      ch === 'b' ? '\b' : ch === 'f' ? '\f' : ch === 'n' ? '\n' :
      ch === 'r' ? '\r' : ch === 't' ? '\t' : undefined;
    if (rep === undefined) {
      throw new JsonSyntaxError(`invalid escape \\${ch}`, this.offset);
    }
    this.strBuf += rep;
    this.mode = 'string';
  }

  private feedUnicode(ch: string): void {
    if (!isHex(ch)) throw new JsonSyntaxError('invalid \\u escape', this.offset);
    this.hexBuf += ch;
    if (this.hexBuf.length === 4) {
      this.strBuf += String.fromCharCode(parseInt(this.hexBuf, 16));
      this.mode = 'string';
    }
  }

  private finishString(events: JsonEvent[]): void {
    this.mode = 'default';
    const s = this.strBuf;
    const top = this.stack[this.stack.length - 1];
    if (top !== undefined && top.kind === 'object' && (top.expect === 'keyOrEnd' || top.expect === 'key')) {
      events.push({ type: 'key', value: s });
      top.expect = 'colon';
      return;
    }
    events.push({ type: 'value', value: s });
    this.valueCompleted();
  }
}

/** One-shot event parse: defined via the incremental machine. */
export function parseJsonEvents(text: string, opts: DecimalOptions = {}): JsonEvent[] {
  const p = new IncrementalJsonParser(opts);
  const events = p.write(text);
  events.push(...p.end());
  return events;
}

/** Reassemble a JSON value tree from an event stream. */
export function buildJsonValue(events: JsonEvent[]): JsonValue {
  const stack: ({ obj: { [k: string]: JsonValue }; key: string | null } | JsonValue[])[] = [];
  let root: JsonValue = null;
  let hasRoot = false;
  const add = (v: JsonValue): void => {
    const top = stack[stack.length - 1];
    if (top === undefined) { root = v; hasRoot = true; return; }
    if (Array.isArray(top)) { top.push(v); return; }
    if (top.key === null) throw new JsonSyntaxError('internal: object value without key', -1);
    top.obj[top.key] = v;
    top.key = null;
  };
  for (const ev of events) {
    switch (ev.type) {
      case 'startObject': stack.push({ obj: {}, key: null }); break;
      case 'startArray': stack.push([]); break;
      case 'endObject': {
        const c = stack.pop();
        if (c === undefined || Array.isArray(c)) throw new JsonSyntaxError('internal: endObject mismatch', -1);
        add(c.obj);
        break;
      }
      case 'endArray': {
        const c = stack.pop();
        if (c === undefined || !Array.isArray(c)) throw new JsonSyntaxError('internal: endArray mismatch', -1);
        add(c);
        break;
      }
      case 'key': {
        const top = stack[stack.length - 1];
        if (top === undefined || Array.isArray(top)) throw new JsonSyntaxError('internal: key outside object', -1);
        top.key = ev.value;
        break;
      }
      case 'value': add(ev.value); break;
    }
  }
  if (!hasRoot) throw new JsonSyntaxError('internal: no root value', -1);
  return root;
}

/** Parse a whole JSON text into a tree; numbers stay exact + raw-preserving. */
export function parseJson(text: string, opts: DecimalOptions = {}): JsonValue {
  return buildJsonValue(parseJsonEvents(text, opts));
}
