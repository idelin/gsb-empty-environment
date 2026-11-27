import {
  LEGACY_ENTITIES,
  NAMED_ENTITIES,
  NUMERIC_REFERENCE_OVERRIDES,
} from './entities.ts';
import type { SourceLocation } from './types.ts';

export interface RawAttribute {
  name: string;
  value: string;
  location: SourceLocation;
}

export interface TokenHandler {
  onText(data: string, location: SourceLocation): void;
  onStartTag(
    tagName: string,
    attributes: RawAttribute[],
    selfClosing: boolean,
    location: SourceLocation,
  ): void;
  onEndTag(tagName: string, location: SourceLocation): void;
  onComment(data: string, location: SourceLocation): void;
  onDoctype(location: SourceLocation): void;
  onEOF(location: SourceLocation): void;
}

const isWhitespace = (ch: string): boolean =>
  ch === '\t' || ch === '\n' || ch === '\f' || ch === '\r' || ch === ' ';
const isAlpha = (ch: string): boolean =>
  (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z');

const MAX_ENTITY_LENGTH = 32;

/**
 * HTML5 tokenizer. Implements the data / raw-text / script / comment /
 * attribute state machine following the W3C "Tokenization" section, so that
 * `script`, `style`, `textarea`, `noscript` and comments consume their
 * contents exactly like a browser would.
 */
export class Tokenizer {
  private readonly input: string;
  private readonly handler: TokenHandler;
  private pos = 0;
  private line = 1;
  private column = 1;
  private state = 'DATA';
  /** Tag name tracked for RAWTEXT / RCDATA / SCRIPT end-tag matching. */
  private rawTag = '';
  private textBuffer = '';
  private tagName = '';
  private attributes: RawAttribute[] = [];
  private currentAttrName = '';
  private currentAttrValue = '';
  private attrLocation: SourceLocation = { offset: 0, line: 1, column: 1 };
  private selfClosing = false;
  private commentBuffer = '';
  private tempBuffer = '';
  private tagStart: SourceLocation = { offset: 0, line: 1, column: 1 };
  /** True while the token under construction is an end-tag token. */
  private isEndTagToken = false;

  constructor(input: string, handler: TokenHandler) {
    this.input = input;
    this.handler = handler;
  }

  run(): void {
    while (this.pos < this.input.length) {
      this.step();
    }
    this.flushText();
    this.handleEOF();
    this.handler.onEOF(this.loc());
  }

  private handleEOF(): void {
    switch (this.state) {
      case 'DATA':
      case 'PLAINTEXT':
        return;
      case 'RCDATA':
      case 'RAWTEXT':
      case 'SCRIPT_DATA':
      case 'SCRIPT_DATA_ESCAPE':
      case 'SCRIPT_DATA_ESCAPE_DASH':
      case 'SCRIPT_DATA_DOUBLE_ESCAPE':
      case 'SCRIPT_DATA_DOUBLE_ESCAPE_DASH':
        this.flushText();
        return;
      case 'BOGUS_COMMENT':
      case 'CDATA_SECTION':
        this.handler.onComment(this.commentBuffer, this.tagStart);
        this.commentBuffer = '';
        return;
      default:
        if (this.state.startsWith('COMMENT') ||
          this.state === 'MARKUP_DECLARATION_OPEN') {
          this.handler.onComment(this.commentBuffer, this.tagStart);
          this.commentBuffer = '';
        }
        return;
    }
  }

  private peek(offset = 0): string {
    return this.input[this.pos + offset] ?? '';
  }

  private loc(): SourceLocation {
    return { offset: this.pos, line: this.line, column: this.column };
  }

  private restorePosition(offset: number): void {
    this.pos = offset;
    let line = 1;
    let column = 1;
    for (let i = 0; i < offset && i < this.input.length; i += 1) {
      if (this.input[i] === '\n') {
        line += 1;
        column = 1;
      } else {
        column += 1;
      }
    }
    this.line = line;
    this.column = column;
  }

  private advance(): string {
    const ch = this.input[this.pos] ?? '';
    this.pos += 1;
    if (ch === '\n') {
      this.line += 1;
      this.column = 1;
    } else {
      this.column += 1;
    }
    return ch;
  }

  private emitChar(ch: string): void {
    this.textBuffer += ch;
  }

  private flushText(): void {
    if (this.textBuffer !== '') {
      this.handler.onText(this.textBuffer, this.loc());
      this.textBuffer = '';
    }
  }

  private step(): void {
    const ch = this.peek();
    switch (this.state) {
      case 'DATA': this.dataState(ch); break;
      case 'RCDATA':
      case 'RAWTEXT': this.rawTextState(ch); break;
      case 'SCRIPT_DATA': this.scriptDataState(ch); break;
      case 'PLAINTEXT':
        this.emitChar(ch === '\0' ? ((this.advance()), '�') : this.advance());
        break;
      case 'TAG_OPEN': this.tagOpenState(ch); break;
      case 'END_TAG_OPEN': this.endTagOpenState(ch); break;
      case 'TAG_NAME': this.tagNameState(ch); break;
      case 'RCDATA_LT':
      case 'RAWTEXT_LT': this.rawTextLtState(ch); break;
      case 'RCDATA_END_TAG_OPEN':
      case 'RAWTEXT_END_TAG_OPEN': this.rawEndTagOpenState(ch); break;
      case 'RCDATA_END_TAG_NAME':
      case 'RAWTEXT_END_TAG_NAME': this.rawEndTagNameState(ch); break;
      case 'SCRIPT_DATA_LT': this.scriptDataLt(ch); break;
      case 'SCRIPT_DATA_END_TAG_OPEN': this.scriptEndTagOpen(ch); break;
      case 'SCRIPT_DATA_END_TAG_NAME': this.scriptEndTagName(ch); break;
      case 'SCRIPT_DATA_ESCAPE': this.scriptEscape(ch); break;
      case 'SCRIPT_DATA_ESCAPE_DASH': this.scriptEscapeDash(ch); break;
      case 'SCRIPT_DATA_ESCAPE_LT': this.scriptEscapeLt(ch); break;
      case 'SCRIPT_DATA_ESCAPE_END_TAG_OPEN':
        this.scriptEscapeEndTagOpen(ch); break;
      case 'SCRIPT_DATA_ESCAPE_END_TAG_NAME':
        this.scriptEscapeEndTagName(ch); break;
      case 'SCRIPT_DATA_DOUBLE_ESCAPE':
        this.scriptDoubleEscape(ch); break;
      case 'SCRIPT_DATA_DOUBLE_ESCAPE_DASH':
        this.scriptDoubleEscapeDash(ch); break;
      case 'SCRIPT_DATA_DOUBLE_ESCAPE_LT':
        this.scriptDoubleEscapeLt(ch); break;
      case 'SCRIPT_DATA_DOUBLE_ESCAPE_END_TAG_OPEN':
        this.scriptDoubleEscapeEndTagOpen(ch); break;
      case 'SCRIPT_DATA_DOUBLE_ESCAPE_END_TAG_NAME':
        this.scriptDoubleEscapeEndTagName(ch); break;
      case 'BEFORE_ATTRIBUTE_NAME': this.beforeAttributeName(ch); break;
      case 'ATTRIBUTE_NAME': this.attributeName(ch); break;
      case 'AFTER_ATTRIBUTE_NAME': this.afterAttributeName(ch); break;
      case 'BEFORE_ATTRIBUTE_VALUE': this.beforeAttributeValue(ch); break;
      case 'ATTRIBUTE_VALUE_DOUBLE_QUOTED':
        this.attributeValueQuoted(ch, '"'); break;
      case 'ATTRIBUTE_VALUE_SINGLE_QUOTED':
        this.attributeValueQuoted(ch, "'"); break;
      case 'ATTRIBUTE_VALUE_UNQUOTED': this.attributeValueUnquoted(ch); break;
      case 'AFTER_ATTRIBUTE_VALUE_QUOTED':
        this.afterAttributeValueQuoted(ch); break;
      case 'SELF_CLOSING_START':
        this.advance();
        this.selfClosing = true;
        this.emitTag();
        break;
      case 'BOGUS_COMMENT': this.bogusComment(ch); break;
      case 'MARKUP_DECLARATION_OPEN': this.markupDeclarationOpen(ch); break;
      case 'COMMENT_START': this.commentStart(ch); break;
      case 'COMMENT_START_DASH': this.commentStartDash(ch); break;
      case 'COMMENT': this.commentState(ch); break;
      case 'COMMENT_LT': this.commentLt(ch); break;
      case 'COMMENT_LT_BANG': this.commentLtBang(ch); break;
      case 'COMMENT_LT_BANG_DASH': this.commentLtBangDash(ch); break;
      case 'COMMENT_LT_BANG_DASH_DASH': this.commentLtBangDashDash(ch); break;
      case 'COMMENT_END_DASH': this.commentEndDash(ch); break;
      case 'COMMENT_END': this.commentEnd(ch); break;
      case 'CDATA_SECTION': this.cdataSection(ch); break;
      case 'DOCTYPE_SKIP': this.doctypeSkip(ch); break;
      default: this.dataState(ch);
    }
  }

  private dataState(ch: string): void {
    if (ch === '&') {
      this.advance();
      this.emitChar(this.consumeCharRef());
    } else if (ch === '<') {
      this.advance();
      this.flushText();
      const start = this.loc();
      this.tagStart = {
        offset: start.offset - 1,
        line: start.line,
        column: start.column - 1,
      };
      this.state = 'TAG_OPEN';
    } else if (ch === '\0') {
      this.advance();
      this.emitChar('�');
    } else {
      this.emitChar(this.advance());
    }
  }

  private rawTextState(ch: string): void {
    const base = this.state;
    if (ch === '<') {
      this.advance();
      this.state = base === 'RCDATA' ? 'RCDATA_LT' : 'RAWTEXT_LT';
    } else if (ch === '\0') {
      this.advance();
      this.emitChar('�');
    } else if (base === 'RCDATA' && ch === '&') {
      this.advance();
      this.emitChar(this.consumeCharRef());
    } else {
      this.emitChar(this.advance());
    }
  }

  private scriptDataState(ch: string): void {
    if (ch === '<') {
      this.advance();
      this.state = 'SCRIPT_DATA_LT';
    } else if (ch === '\0') {
      this.advance();
      this.emitChar('�');
    } else {
      this.emitChar(this.advance());
    }
  }

  private tagOpenState(ch: string): void {
    if (ch === '!') {
      this.advance();
      this.state = 'MARKUP_DECLARATION_OPEN';
    } else if (ch === '/') {
      this.advance();
      this.state = 'END_TAG_OPEN';
    } else if (isAlpha(ch)) {
      this.tagName = this.advance().toLowerCase();
      this.attributes = [];
      this.selfClosing = false;
      this.state = 'TAG_NAME';
    } else if (ch === '?') {
      this.advance();
      this.commentBuffer = '';
      this.state = 'BOGUS_COMMENT';
    } else {
      this.emitChar('<');
      this.state = 'DATA';
    }
  }

  private endTagOpenState(ch: string): void {
    if (isAlpha(ch)) {
      this.tagName = this.advance().toLowerCase();
      this.isEndTagToken = true;
      this.state = 'TAG_NAME';
    } else if (ch === '>') {
      this.advance();
      this.emitChar('</');
      this.state = 'DATA';
    } else if (ch === '\0') {
      this.advance();
      this.commentBuffer = '';
      this.state = 'BOGUS_COMMENT';
    } else {
      this.advance();
      this.commentBuffer = '';
      this.state = 'BOGUS_COMMENT';
    }
  }

  private tagNameState(ch: string): void {
    if (isWhitespace(ch)) {
      this.advance();
      this.state = this.isEndTagToken
        ? 'BEFORE_ATTRIBUTE_NAME'
        : 'BEFORE_ATTRIBUTE_NAME';
    } else if (ch === '/') {
      this.advance();
      this.state = 'SELF_CLOSING_START';
    } else if (ch === '>') {
      this.advance();
      this.emitTag();
    } else if (ch === '\0') {
      this.advance();
      this.tagName += '�';
    } else {
      this.tagName += this.advance().toLowerCase();
    }
  }

  private emitTag(): void {
    const name = this.tagName;
    const attrs = this.attributes;
    const selfClosing = this.selfClosing;
    const start = this.tagStart;
    const endToken = this.isEndTagToken;
    if (endToken) {
      this.handler.onEndTag(name, start);
    } else {
      this.handler.onStartTag(name, attrs, selfClosing, start);
    }
    this.tagName = '';
    this.attributes = [];
    this.selfClosing = false;
    this.isEndTagToken = false;
    this.state = 'DATA';
  }

  private emitEndTag(): void {
    const name = this.tagName;
    const start = this.tagStart;
    this.handler.onEndTag(name, start);
    this.tagName = '';
    this.isEndTagToken = false;
    this.state = 'DATA';
  }

  /**
   * Switch the tokenizer content model after the tree builder inserts a
   * raw-text / RCDATA / script element (HTML5 "tree construction dispatch").
   */
  setContentModel(
    tagName: string,
    model: 'data' | 'rawtext' | 'rcdata' | 'scriptdata' | 'plaintext',
  ): void {
    this.rawTag = tagName;
    if (model === 'rawtext') this.state = 'RAWTEXT';
    else if (model === 'rcdata') this.state = 'RCDATA';
    else if (model === 'scriptdata') this.state = 'SCRIPT_DATA';
    else if (model === 'plaintext') this.state = 'PLAINTEXT';
    else this.state = 'DATA';
  }

  private beforeAttributeName(ch: string): void {
    if (isWhitespace(ch)) {
      this.advance();
    } else if (ch === '/') {
      this.advance();
      this.state = 'SELF_CLOSING_START';
    } else if (ch === '>') {
      this.advance();
      this.emitTag();
    } else if (ch === '\0') {
      this.advance();
      this.startAttribute('�');
      this.state = 'ATTRIBUTE_NAME';
    } else if (ch === '"' || ch === "'" || ch === '<') {
      this.startAttribute(this.advance());
      this.state = 'ATTRIBUTE_NAME';
    } else {
      this.startAttribute(this.advance());
      this.state = 'ATTRIBUTE_NAME';
    }
  }

  private startAttribute(firstChar: string): void {
    this.currentAttrName = firstChar;
    this.currentAttrValue = '';
    this.attrLocation = this.loc();
    this.attrLocation.column -= 1;
    this.attrLocation.offset -= 1;
  }

  private attributeName(ch: string): void {
    if (isWhitespace(ch)) {
      this.advance();
      this.state = 'AFTER_ATTRIBUTE_NAME';
    } else if (ch === '/') {
      this.advance();
      this.finishAttribute();
      this.state = 'SELF_CLOSING_START';
    } else if (ch === '=') {
      this.advance();
      this.state = 'BEFORE_ATTRIBUTE_VALUE';
    } else if (ch === '>') {
      this.advance();
      this.finishAttribute();
      this.emitTag();
    } else if (ch === '\0') {
      this.advance();
      this.currentAttrName += '�';
    } else if (ch === '"' || ch === "'" || ch === '<') {
      this.currentAttrName += this.advance();
    } else {
      this.currentAttrName += this.advance().toLowerCase();
    }
  }

  private afterAttributeName(ch: string): void {
    if (isWhitespace(ch)) {
      this.advance();
    } else if (ch === '/') {
      this.advance();
      this.state = 'SELF_CLOSING_START';
    } else if (ch === '=') {
      this.advance();
      this.state = 'BEFORE_ATTRIBUTE_VALUE';
    } else if (ch === '>') {
      this.advance();
      this.finishAttribute();
      this.emitTag();
    } else if (ch === '\0') {
      this.advance();
      this.startAttribute('�');
      this.state = 'ATTRIBUTE_NAME';
    } else if (ch === '"' || ch === "'" || ch === '<') {
      this.startAttribute(this.advance());
      this.state = 'ATTRIBUTE_NAME';
    } else {
      this.finishAttribute();
      this.startAttribute(this.advance());
      this.state = 'ATTRIBUTE_NAME';
    }
  }

  private finishAttribute(): void {
    if (this.currentAttrName === '') {
      return;
    }
    if (
      !this.attributes.some((attr) => attr.name === this.currentAttrName)
    ) {
      this.attributes.push({
        name: this.currentAttrName,
        value: this.currentAttrValue,
        location: this.attrLocation,
      });
    }
    this.currentAttrName = '';
    this.currentAttrValue = '';
  }

  private beforeAttributeValue(ch: string): void {
    if (isWhitespace(ch)) {
      this.advance();
    } else if (ch === '"') {
      this.advance();
      this.currentAttrValue = '';
      this.state = 'ATTRIBUTE_VALUE_DOUBLE_QUOTED';
    } else if (ch === "'") {
      this.advance();
      this.currentAttrValue = '';
      this.state = 'ATTRIBUTE_VALUE_SINGLE_QUOTED';
    } else if (ch === '\0') {
      this.advance();
      this.currentAttrValue = '�';
      this.state = 'ATTRIBUTE_VALUE_UNQUOTED';
    } else if (ch === '>') {
      this.advance();
      this.finishAttribute();
      this.emitTag();
    } else {
      this.currentAttrValue = this.advance();
      this.state = 'ATTRIBUTE_VALUE_UNQUOTED';
    }
  }

  private attributeValueQuoted(ch: string, quote: string): void {
    if (ch === quote) {
      this.advance();
      this.finishAttribute();
      this.state = 'AFTER_ATTRIBUTE_VALUE_QUOTED';
    } else if (ch === '&') {
      this.advance();
      this.currentAttrValue += this.consumeCharRef();
    } else if (ch === '\0') {
      this.advance();
      this.currentAttrValue += '�';
    } else {
      this.currentAttrValue += this.advance();
    }
  }

  private attributeValueUnquoted(ch: string): void {
    if (isWhitespace(ch)) {
      this.advance();
      this.finishAttribute();
      this.state = 'BEFORE_ATTRIBUTE_NAME';
    } else if (ch === '&') {
      this.advance();
      this.currentAttrValue += this.consumeCharRef();
    } else if (ch === '>') {
      this.advance();
      this.finishAttribute();
      this.emitTag();
    } else if (ch === '\0') {
      this.advance();
      this.currentAttrValue += '�';
    } else if (ch === '"' || ch === "'" || ch === '<' || ch === '`') {
      this.currentAttrValue += this.advance();
    } else {
      this.currentAttrValue += this.advance();
    }
  }

  private afterAttributeValueQuoted(ch: string): void {
    if (isWhitespace(ch)) {
      this.advance();
      this.state = 'BEFORE_ATTRIBUTE_NAME';
    } else if (ch === '/') {
      this.advance();
      this.state = 'SELF_CLOSING_START';
    } else if (ch === '>') {
      this.advance();
      this.emitTag();
    } else if (ch === '\0') {
      this.advance();
      this.startAttribute('�');
      this.state = 'ATTRIBUTE_NAME';
    } else {
      this.startAttribute(this.advance());
      this.state = 'ATTRIBUTE_NAME';
    }
  }

  private rawTextLtState(ch: string): void {
    const base = this.state === 'RCDATA_LT' ? 'RCDATA' : 'RAWTEXT';
    if (ch === '/') {
      this.advance();
      this.tempBuffer = '';
      this.tagName = '';
      this.state =
        base === 'RCDATA'
          ? 'RCDATA_END_TAG_OPEN'
          : 'RAWTEXT_END_TAG_OPEN';
    } else {
      this.emitChar('<');
      this.state = base;
      this.step();
    }
  }

  private rawEndTagOpenState(ch: string): void {
    const base = this.state.startsWith('RCDATA') ? 'RCDATA' : 'RAWTEXT';
    if (isAlpha(ch)) {
      this.tempBuffer = '';
      this.tagName = '';
      this.startEndTagBuffer(ch);
      this.state =
        base === 'RCDATA'
          ? 'RCDATA_END_TAG_NAME'
          : 'RAWTEXT_END_TAG_NAME';
    } else {
      this.emitChar('<');
      this.emitChar('/');
      this.state = base;
      this.step();
    }
  }

  private startEndTagBuffer(ch: string): void {
    const lower = ch.toLowerCase();
    this.advance();
    this.tempBuffer += lower;
    this.tagName += lower;
  }

  private rawEndTagNameState(ch: string): void {
    const base = this.state.startsWith('RCDATA') ? 'RCDATA' : 'RAWTEXT';
    if (isWhitespace(ch) || ch === '/' || ch === '>') {
      if (this.tagName === this.rawTag) {
        if (isWhitespace(ch)) {
          this.advance();
          this.attributes = [];
          this.selfClosing = false;
          this.state = 'BEFORE_ATTRIBUTE_NAME';
        } else if (ch === '/') {
          this.advance();
          this.state = 'SELF_CLOSING_START';
        } else {
          this.advance();
          this.emitEndTag();
        }
        return;
      }
      this.emitChar('<');
      this.emitChar('/');
      this.emitChar(this.tempBuffer);
      this.tempBuffer = '';
      this.tagName = '';
      this.state = base;
      this.step();
    } else if (isAlpha(ch)) {
      this.startEndTagBuffer(ch);
    } else {
      this.emitChar('<');
      this.emitChar('/');
      this.emitChar(this.tempBuffer);
      this.tempBuffer = '';
      this.tagName = '';
      this.state = base;
      this.step();
    }
  }

  private scriptDataLt(ch: string): void {
    if (ch === '/') {
      this.advance();
      this.tempBuffer = '';
      this.tagName = '';
      this.state = 'SCRIPT_DATA_END_TAG_OPEN';
    } else if (isAlpha(ch)) {
      this.emitChar('<');
      this.emitChar(this.advance());
      this.state = 'SCRIPT_DATA';
      this.step();
    } else {
      this.emitChar('<');
      this.state = 'SCRIPT_DATA';
      this.step();
    }
  }

  private scriptEndTagOpen(ch: string): void {
    if (isAlpha(ch)) {
      this.tempBuffer = '';
      this.tagName = '';
      this.startEndTagBuffer(ch);
      this.state = 'SCRIPT_DATA_END_TAG_NAME';
    } else {
      this.emitChar('<');
      this.emitChar('/');
      this.state = 'SCRIPT_DATA';
      this.step();
    }
  }

  private scriptEndTagName(ch: string): void {
    if (isWhitespace(ch) || ch === '/' || ch === '>') {
      if (this.tagName === 'script') {
        if (isWhitespace(ch)) {
          this.advance();
          this.attributes = [];
          this.selfClosing = false;
          this.state = 'BEFORE_ATTRIBUTE_NAME';
        } else if (ch === '/') {
          this.advance();
          this.state = 'SELF_CLOSING_START';
        } else {
          this.advance();
          this.emitEndTag();
        }
        return;
      }
      this.emitChar('<');
      this.emitChar('/');
      this.emitChar(this.tempBuffer);
      this.tempBuffer = '';
      this.tagName = '';
      this.state = 'SCRIPT_DATA';
      this.step();
    } else if (isAlpha(ch)) {
      this.startEndTagBuffer(ch);
    } else {
      this.emitChar('<');
      this.emitChar('/');
      this.emitChar(this.tempBuffer);
      this.tempBuffer = '';
      this.tagName = '';
      this.state = 'SCRIPT_DATA';
      this.step();
    }
  }

  private scriptEscape(ch: string): void {
    if (ch === '-') {
      this.advance();
      this.emitChar(ch);
      this.state = 'SCRIPT_DATA_ESCAPE_DASH';
    } else if (ch === '<') {
      this.advance();
      this.emitChar(ch);
      this.state = 'SCRIPT_DATA_ESCAPE_LT';
    } else if (ch === '\0') {
      this.advance();
      this.emitChar('�');
    } else {
      this.emitChar(this.advance());
    }
  }

  private scriptEscapeDash(ch: string): void {
    if (ch === '-') {
      this.advance();
      this.emitChar(ch);
      this.state = 'SCRIPT_DATA_ESCAPE_DASH';
    } else if (ch === '<') {
      this.advance();
      this.emitChar(ch);
      this.state = 'SCRIPT_DATA_ESCAPE_LT';
    } else if (ch === '\0') {
      this.advance();
      this.emitChar('�');
      this.state = 'SCRIPT_DATA_ESCAPE';
    } else {
      this.emitChar(this.advance());
      this.state = 'SCRIPT_DATA_ESCAPE';
    }
  }

  private scriptEscapeLt(ch: string): void {
    if (ch === '/') {
      this.advance();
      this.emitChar(ch);
      this.tempBuffer = '';
      this.tagName = '';
      this.state = 'SCRIPT_DATA_ESCAPE_END_TAG_OPEN';
    } else if (isAlpha(ch)) {
      this.emitChar(this.advance());
      this.state = 'SCRIPT_DATA_ESCAPE';
      this.step();
    } else {
      this.state = 'SCRIPT_DATA_ESCAPE';
      this.step();
    }
  }

  private scriptEscapeEndTagOpen(ch: string): void {
    if (isAlpha(ch)) {
      this.tempBuffer = '';
      this.tagName = '';
      this.startEndTagBuffer(ch);
      this.state = 'SCRIPT_DATA_ESCAPE_END_TAG_NAME';
    } else {
      this.state = 'SCRIPT_DATA_ESCAPE';
      this.step();
    }
  }

  private scriptEscapeEndTagName(ch: string): void {
    if (isWhitespace(ch) || ch === '/' || ch === '>') {
      if (this.tagName === 'script') {
        if (isWhitespace(ch)) {
          this.advance();
          this.attributes = [];
          this.selfClosing = false;
          this.state = 'BEFORE_ATTRIBUTE_NAME';
        } else if (ch === '/') {
          this.advance();
          this.state = 'SELF_CLOSING_START';
        } else {
          this.advance();
          this.emitEndTag();
        }
        return;
      }
      this.state = 'SCRIPT_DATA_ESCAPE';
      this.step();
    } else if (isAlpha(ch)) {
      this.startEndTagBuffer(ch);
    } else {
      this.state = 'SCRIPT_DATA_ESCAPE';
      this.step();
    }
  }

  private scriptDoubleEscape(ch: string): void {
    if (ch === '"') {
      this.advance();
      this.emitChar(ch);
    } else if (ch === '\0') {
      this.advance();
      this.emitChar('�');
      this.state = 'SCRIPT_DATA_ESCAPE';
    } else if (ch === '-') {
      this.advance();
      this.emitChar(ch);
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE_DASH';
    } else if (ch === '<') {
      this.advance();
      this.emitChar(ch);
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE_LT';
    } else {
      this.emitChar(this.advance());
      this.state = 'SCRIPT_DATA_ESCAPE';
    }
  }

  private scriptDoubleEscapeDash(ch: string): void {
    if (ch === '-') {
      this.advance();
      this.emitChar(ch);
    } else if (ch === '<') {
      this.advance();
      this.emitChar(ch);
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE_LT';
    } else if (ch === '\0') {
      this.advance();
      this.emitChar('�');
      this.state = 'SCRIPT_DATA_ESCAPE';
    } else {
      this.emitChar(this.advance());
      this.state = 'SCRIPT_DATA_ESCAPE';
    }
  }

  private scriptDoubleEscapeLt(ch: string): void {
    if (ch === '/') {
      this.advance();
      this.emitChar(ch);
      this.tempBuffer = '';
      this.tagName = '';
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE_END_TAG_OPEN';
    } else if (isAlpha(ch)) {
      this.emitChar(this.advance());
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE';
      this.step();
    } else {
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE';
      this.step();
    }
  }

  private scriptDoubleEscapeEndTagOpen(ch: string): void {
    if (isAlpha(ch)) {
      this.tempBuffer = '';
      this.tagName = '';
      this.startEndTagBuffer(ch);
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE_END_TAG_NAME';
    } else {
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE';
      this.step();
    }
  }

  private scriptDoubleEscapeEndTagName(ch: string): void {
    if (isWhitespace(ch) || ch === '/' || ch === '>') {
      if (this.tagName === 'script') {
        if (isWhitespace(ch)) {
          this.advance();
          this.attributes = [];
          this.selfClosing = false;
          this.state = 'BEFORE_ATTRIBUTE_NAME';
        } else if (ch === '/') {
          this.advance();
          this.state = 'SELF_CLOSING_START';
        } else {
          this.advance();
          this.emitEndTag();
        }
        return;
      }
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE';
      this.step();
    } else if (isAlpha(ch)) {
      this.startEndTagBuffer(ch);
    } else {
      this.state = 'SCRIPT_DATA_DOUBLE_ESCAPE';
      this.step();
    }
  }

  private bogusComment(ch: string): void {
    if (ch === '>') {
      this.advance();
      this.handler.onComment(this.commentBuffer, this.tagStart);
      this.commentBuffer = '';
      this.state = 'DATA';
    } else if (ch === '\0') {
      this.advance();
      this.commentBuffer += '�';
    } else {
      this.commentBuffer += this.advance();
    }
  }

  private markupDeclarationOpen(ch: string): void {
    if (this.input.startsWith('--', this.pos)) {
      this.advance();
      this.advance();
      this.commentBuffer = '';
      this.state = 'COMMENT_START';
    } else if (this.input.toLowerCase().startsWith('doctype', this.pos)) {
      for (let i = 0; i < 7; i += 1) {
        this.advance();
      }
      this.commentBuffer = '';
      this.state = 'DOCTYPE_SKIP';
    } else if (this.input.startsWith('[CDATA[', this.pos)) {
      for (let i = 0; i < 7; i += 1) {
        this.advance();
      }
      this.commentBuffer = '';
      this.state = 'CDATA_SECTION';
    } else {
      this.advance();
      this.commentBuffer = '';
      this.state = 'BOGUS_COMMENT';
    }
  }

  private doctypeSkip(ch: string): void {
    if (ch === '>') {
      this.advance();
      this.handler.onDoctype(this.tagStart);
      this.commentBuffer = '';
      this.state = 'DATA';
    } else if (ch === '\0') {
      this.advance();
      this.commentBuffer += '�';
    } else {
      this.commentBuffer += this.advance();
    }
  }

  private cdataSection(ch: string): void {
    if (this.input.startsWith(']]>', this.pos)) {
      for (let i = 0; i < 3; i += 1) {
        this.advance();
      }
      // In HTML, CDATA outside foreign content behaves as a bogus comment.
      this.handler.onComment('[CDATA[' + this.commentBuffer + ']]>',
        this.tagStart);
      this.commentBuffer = '';
      this.state = 'DATA';
    } else if (ch === '\0') {
      this.advance();
      this.commentBuffer += '�';
    } else {
      this.commentBuffer += this.advance();
    }
  }

  private commentStart(ch: string): void {
    if (ch === '-') {
      this.advance();
      this.state = 'COMMENT_START_DASH';
    } else if (ch === '>') {
      this.advance();
      this.handler.onComment(this.commentBuffer, this.tagStart);
      this.commentBuffer = '';
      this.state = 'DATA';
    } else {
      this.commentBuffer += ch;
      this.advance();
      this.state = 'COMMENT';
    }
  }

  private commentStartDash(ch: string): void {
    if (ch === '-') {
      this.advance();
      this.state = 'COMMENT_LT_BANG_DASH_DASH';
    } else if (ch === '>') {
      this.advance();
      this.handler.onComment(this.commentBuffer, this.tagStart);
      this.commentBuffer = '';
      this.state = 'DATA';
    } else {
      this.commentBuffer += '-';
      this.commentBuffer += ch;
      this.advance();
      this.state = 'COMMENT';
    }
  }

  private commentState(ch: string): void {
    if (ch === '-') {
      this.advance();
      this.state = 'COMMENT_END_DASH';
    } else if (ch === '<') {
      this.commentBuffer += this.advance();
      this.state = 'COMMENT_LT';
    } else if (ch === '\0') {
      this.advance();
      this.commentBuffer += '�';
    } else {
      this.commentBuffer += this.advance();
    }
  }

  private commentLt(ch: string): void {
    if (ch === '<') {
      this.commentBuffer += this.advance();
      this.state = 'COMMENT_LT';
    } else if (ch === '!') {
      this.commentBuffer += this.advance();
      this.state = 'COMMENT_LT_BANG';
    } else {
      this.state = 'COMMENT';
      this.step();
    }
  }

  private commentLtBang(ch: string): void {
    if (ch === '-') {
      this.commentBuffer += this.advance();
      this.state = 'COMMENT_LT_BANG_DASH';
    } else {
      this.state = 'COMMENT';
      this.step();
    }
  }

  private commentLtBangDash(ch: string): void {
    if (ch === '-') {
      this.advance();
      this.state = 'COMMENT_LT_BANG_DASH_DASH';
    } else {
      this.state = 'COMMENT';
      this.step();
    }
  }

  private commentEndDash(ch: string): void {
    if (ch === '-') {
      this.advance();
      this.state = 'COMMENT_END';
    } else {
      this.commentBuffer += '-';
      this.state = 'COMMENT';
      this.step();
    }
  }

  private commentLtBangDashDash(ch: string): void {
    if (ch === '>') {
      this.advance();
      this.handler.onComment(this.commentBuffer, this.tagStart);
      this.commentBuffer = '';
      this.state = 'DATA';
    } else if (ch === '\0') {
      this.advance();
      this.commentBuffer += '�';
      this.state = 'COMMENT';
    } else if (ch === '-') {
      this.commentBuffer += this.advance();
    } else {
      this.commentBuffer += this.advance();
      this.state = 'COMMENT';
    }
  }

  private commentEnd(ch: string): void {
    if (ch === '>') {
      this.advance();
      this.handler.onComment(this.commentBuffer, this.tagStart);
      this.commentBuffer = '';
      this.state = 'DATA';
    } else if (ch === '\0') {
      this.advance();
      this.commentBuffer += '�';
      this.state = 'COMMENT';
    } else if (ch === '-') {
      this.commentBuffer += this.advance();
    } else if (ch === '!') {
      this.advance();
      this.state = 'COMMENT_LT_BANG_DASH_DASH';
    } else {
      this.commentBuffer += '-';
      this.commentBuffer += '-';
      this.state = 'COMMENT';
      this.step();
    }
  }

  /**
   * Consume a character reference. The `&` has already been consumed.
   * Implements the HTML5 "consume a character reference" algorithm; unknown
   * references return a literal `&` and leave the following bytes untouched.
   */
  private consumeCharRef(): string {
    const start = this.pos;
    const next = this.peek();
    if (!isAlpha(next) && next !== '#') {
      return '&';
    }
    if (next === '#') {
      return this.consumeNumericRef(start);
    }
    let name = '';
    while (this.pos < this.input.length && name.length <= MAX_ENTITY_LENGTH) {
      const ch = this.peek();
      if (!/[A-Za-z0-9]/.test(ch)) {
        break;
      }
      name += ch;
      this.advance();
    }
    const hasSemicolon = this.peek() === ';';
    if (hasSemicolon) {
      const resolved = NAMED_ENTITIES[name];
      if (resolved !== undefined) {
        this.advance();
        return resolved;
      }
    } else {
      const resolved = LEGACY_ENTITIES[name];
      if (resolved !== undefined && this.legacyBoundaryAllowed()) {
        return resolved;
      }
    }
    // No match: rewind and treat as literal.
    this.restorePosition(start);
    return '&';
  }

  /** Legacy non-semicolon entities only decode before specific boundaries. */
  private legacyBoundaryAllowed(): boolean {
    const ch = this.peek();
    if (ch === '') {
      return true;
    }
    return !/[A-Za-z0-9]/.test(ch);
  }

  private consumeNumericRef(start: number): string {
    this.advance(); // '#'
    let hex = false;
    if (this.peek() === 'x' || this.peek() === 'X') {
      hex = true;
      this.advance();
    }
    let digits = '';
    while (this.pos < this.input.length && digits.length < 8) {
      const ch = this.peek();
      if (hex ? /[0-9a-fA-F]/.test(ch) : /[0-9]/.test(ch)) {
        digits += ch;
        this.advance();
      } else {
        break;
      }
    }
    if (digits === '') {
      this.restorePosition(start);
      return '&';
    }
    if (this.peek() === ';') {
      this.advance();
    }
    let codePoint = parseInt(digits, hex ? 16 : 10);
    if (Number.isNaN(codePoint) || codePoint > 0x10ffff) {
      return '�';
    }
    const override = NUMERIC_REFERENCE_OVERRIDES[codePoint];
    if (override !== undefined) {
      codePoint = override;
    } else if (
      (codePoint >= 0x01 && codePoint <= 0x08) ||
      (codePoint >= 0x0e && codePoint <= 0x1f) ||
      (codePoint >= 0x7f && codePoint <= 0x9f) ||
      (codePoint >= 0xfdd0 && codePoint <= 0xfdef) ||
      (codePoint & 0xfffe) === 0xfffe ||
      (codePoint >= 0xd800 && codePoint <= 0xdfff)
    ) {
      return '�';
    }
    try {
      return String.fromCodePoint(codePoint);
    } catch {
      return '�';
    }
  }
}
