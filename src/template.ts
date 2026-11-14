import { TemplateError } from './errors.ts';
import {
  countExpressionNodes,
  parseExpression,
  type Expression,
  type ExpressionBudget,
} from './expression.ts';
import { buildLineStarts, locationAt, type SourceLocation } from './location.ts';
import { assertSafeKey } from './safe.ts';
import type { NormalizedLimits } from './options.ts';

export interface TextNode {
  type: 'text';
  value: string;
  location: SourceLocation;
}

export interface OutputNode {
  type: 'output';
  expression: Expression;
  location: SourceLocation;
}

export type BlockKind = 'if' | 'unless' | 'each' | 'with';

export interface BlockNode {
  type: BlockKind;
  expression: Expression;
  alias: string | null;
  body: TemplateNode[];
  alternate: TemplateNode[];
  location: SourceLocation;
  openOffset: number;
}

export type TemplateNode = TextNode | OutputNode | BlockNode;

export interface TemplateAst {
  nodes: TemplateNode[];
  nodeCount: number;
}

interface TemplateToken {
  type: 'text' | 'tag';
  value: string;
  offset: number;
  triple: boolean;
}

interface ParseState {
  source: string;
  budget: ExpressionBudget;
  maxDepth: number;
  nodeCount: number;
  location(offset: number): SourceLocation;
}

function countNode(state: ParseState, offset: number): void {
  state.nodeCount += 1;
  if (state.nodeCount > state.budget.maxNodes) {
    throw new TemplateError(state.location(offset), 'BUDGET_EXCEEDED');
  }
}

function findTagEnd(
  source: string,
  start: number,
  triple: boolean,
  commentBlock: boolean,
  simpleComment: boolean,
): number {
  if (commentBlock) {
    return source.indexOf('--}}', start);
  }
  if (simpleComment) {
    return source.indexOf('}}', start);
  }

  const close = triple ? '}}}' : '}}';
  let quote: string | null = null;
  for (let index = start; index < source.length; index += 1) {
    const character = source[index];
    if (quote !== null) {
      if (character === '\\') {
        index += 1;
      } else if (character === quote) {
        quote = null;
      }
      continue;
    }
    if (character === "'" || character === '"') {
      quote = character;
      continue;
    }
    if (source.startsWith(close, index)) {
      return index;
    }
  }
  return -1;
}

function tokenizeTemplate(
  source: string,
  state: ParseState,
): TemplateToken[] {
  const tokens: TemplateToken[] = [];
  let index = 0;

  while (index < source.length) {
    const start = source.indexOf('{{', index);
    if (start === -1) {
      if (index < source.length) {
        tokens.push({
          type: 'text',
          value: source.slice(index),
          offset: index,
          triple: false,
        });
      }
      break;
    }

    if (start > index) {
      tokens.push({
        type: 'text',
        value: source.slice(index, start),
        offset: index,
        triple: false,
      });
    }

    const triple = source.startsWith('{{{', start);
    const commentBlock = !triple && source.startsWith('{{!--', start);
    const simpleComment = !triple && !commentBlock && source.startsWith('{{!', start);
    const contentStart = start + (triple ? 3 : 2);
    const close = findTagEnd(
      source,
      contentStart,
      triple,
      commentBlock,
      simpleComment,
    );
    if (close === -1) {
      throw new TemplateError(state.location(start), 'SYNTAX');
    }
    const contentEnd = close;
    const rawContent = source.slice(contentStart, contentEnd);
    const leading = rawContent.length - rawContent.trimStart().length;
    const content = rawContent.trim();
    const contentOffset = contentStart + leading;
    const closeEnd = close + (commentBlock ? 4 : triple ? 3 : 2);

    if (!commentBlock && !content.startsWith('!')) {
      tokens.push({
        type: 'tag',
        value: content,
        offset: contentOffset,
        triple,
      });
    }

    index = closeEnd;
  }

  return tokens;
}

function expressionText(
  text: string,
  offset: number,
  state: ParseState,
): Expression {
  const trimmed = text.trim();
  if (trimmed.length === 0) {
    throw new TemplateError(state.location(offset), 'SYNTAX');
  }
  return parseExpression(trimmed, offset + text.indexOf(trimmed), state.budget);
}

interface AliasSplit {
  expression: string;
  expressionOffset: number;
  alias: string | null;
  aliasOffset: number;
}

function splitAlias(text: string, offset: number): AliasSplit {
  // Find a top-level ` as ` boundary, ignoring strings and grouped expressions.
  let quoteAgain: string | null = null;
  let depthAgain = 0;
  for (let index = 0; index < text.length - 3; index += 1) {
    const character = text[index];
    if (quoteAgain !== null) {
      if (character === '\\') index += 1;
      else if (character === quoteAgain) quoteAgain = null;
      continue;
    }
    if (character === "'" || character === '"') {
      quoteAgain = character;
      continue;
    }
    if (character === '(' || character === '[') depthAgain += 1;
    else if (character === ')' || character === ']') depthAgain = Math.max(0, depthAgain - 1);
    else if (
      depthAgain === 0 &&
      /\s/.test(character) &&
      text.slice(index + 1, index + 3) === 'as' &&
      index + 3 < text.length &&
      /\s/.test(text[index + 3])
    ) {
      let aliasStart = index + 3;
      while (aliasStart < text.length && /\s/.test(text[aliasStart])) {
        aliasStart += 1;
      }
      const alias = text.slice(aliasStart).trim();
      if (!/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(alias)) {
        break;
      }
      return {
        expression: text.slice(0, index).trim(),
        expressionOffset: offset,
        alias,
        aliasOffset: offset + aliasStart,
      };
    }
  }

  return {
    expression: text.trim(),
    expressionOffset: offset + text.indexOf(text.trim()),
    alias: null,
    aliasOffset: offset,
  };
}

function parseBlockHeader(
  tag: string,
  tagOffset: number,
  state: ParseState,
): { kind: BlockKind; expression: Expression; alias: string | null } {
  const hashIndex = tag.indexOf('#');
  const afterHash = tag.slice(hashIndex + 1).trimStart();
  const keywordMatch = /^[A-Za-z_$][A-Za-z0-9_$]*/.exec(afterHash);
  if (keywordMatch === null) {
    throw new TemplateError(state.location(tagOffset), 'SYNTAX');
  }
  const keyword = keywordMatch[0];
  if (
    keyword !== 'if' &&
    keyword !== 'unless' &&
    keyword !== 'each' &&
    keyword !== 'with'
  ) {
    throw new TemplateError(state.location(tagOffset), 'FORBIDDEN_ACCESS');
  }

  const keywordStart = tag.indexOf(keyword, hashIndex + 1);
  const restStartInTag = keywordStart + keyword.length;
  const rest = tag.slice(restStartInTag);
  const relative = restStartInTag + (rest.length - rest.trimStart().length);
  const restText = rest.trim();
  if (restText.length === 0) {
    throw new TemplateError(state.location(tagOffset), 'SYNTAX');
  }
  const expressionOffset = tagOffset + relative;

  if (keyword === 'each' || keyword === 'with') {
    const split = splitAlias(restText, expressionOffset);
    if (split.alias !== null) {
      assertSafeKey(split.alias, state.location(tagOffset + relative + restText.indexOf('as')));
    }
    const expression = expressionText(split.expression, expressionOffset, state);
    return { kind: keyword as BlockKind, expression, alias: split.alias };
  }

  const expression = expressionText(restText, expressionOffset, state);
  return { kind: keyword as BlockKind, expression, alias: null };
}

interface BlockFrame {
  kind: BlockKind;
  rootNode: BlockNode;
  tailNode: BlockNode;
  parentOutput: TemplateNode[];
  elseUsed: boolean;
  openOffset: number;
}

function parseTemplate(
  source: string,
  budget: ExpressionBudget,
  maxDepth: number,
): TemplateAst {
  const state: ParseState = {
    source,
    budget,
    maxDepth,
    nodeCount: 0,
    location: (offset) => locationAt(budget.lineStarts, offset),
  };

  const tokens = tokenizeTemplate(source, state);
  const root: TemplateNode[] = [];
  let activeOutput = root;
  const stack: BlockFrame[] = [];

  for (const token of tokens) {
    if (token.type === 'text') {
      if (token.value.length > 0) {
        countNode(state, token.offset);
        activeOutput.push({
          type: 'text',
          value: token.value,
          location: state.location(token.offset),
        });
      }
      continue;
    }

    const tag = token.value;
    if (tag.startsWith('#')) {
      const header = parseBlockHeader(tag, token.offset, state);
      countNode(state, token.offset);
      if (stack.length >= maxDepth) {
        throw new TemplateError(state.location(token.offset), 'BUDGET_EXCEEDED');
      }

      const node: BlockNode = {
        type: header.kind,
        expression: header.expression,
        alias: header.alias,
        body: [],
        alternate: [],
        location: state.location(token.offset),
        openOffset: token.offset,
      };
      activeOutput.push(node);
      stack.push({
        kind: header.kind,
        rootNode: node,
        tailNode: node,
        parentOutput: activeOutput,
        elseUsed: false,
        openOffset: token.offset,
      });
      activeOutput = node.body;
      continue;
    }

    if (tag.startsWith('/')) {
      const closeName = tag.slice(1).trim();
      if (!/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(closeName)) {
        throw new TemplateError(state.location(token.offset), 'SYNTAX');
      }
      const frame = stack[stack.length - 1];
      if (frame === undefined || frame.kind !== closeName) {
        throw new TemplateError(state.location(token.offset), 'SYNTAX');
      }
      stack.pop();
      activeOutput = frame.parentOutput;
      continue;
    }

    const elseMatch = /^else(?:\s+([\s\S]*))?$/.exec(tag);
    if (elseMatch !== null) {
      const frame = stack[stack.length - 1];
      if (frame === undefined) {
        throw new TemplateError(state.location(token.offset), 'SYNTAX');
      }
      if (frame.elseUsed) {
        throw new TemplateError(state.location(token.offset), 'SYNTAX');
      }
      frame.elseUsed = true;

      const rest = (elseMatch[1] ?? '').trim();
      if (rest.length === 0) {
        activeOutput = frame.tailNode.alternate;
        continue;
      }

      if (!rest.startsWith('if ') || rest.slice(3).trim().length === 0) {
        throw new TemplateError(state.location(token.offset), 'SYNTAX');
      }
      const offsetInTag = tag.indexOf(rest);
      const expressionOffset = token.offset + offsetInTag + 3;
      const test = expressionText(rest.slice(3), expressionOffset, state);
      countNode(state, token.offset);

      const nested: BlockNode = {
        type: 'if',
        expression: test,
        alias: null,
        body: [],
        alternate: [],
        location: state.location(token.offset),
        openOffset: token.offset,
      };
      frame.tailNode.alternate.push(nested);
      frame.tailNode = nested;
      frame.elseUsed = false;
      activeOutput = nested.body;
      continue;
    }

    if (tag.startsWith('>')) {
      throw new TemplateError(state.location(token.offset), 'FORBIDDEN_ACCESS');
    }
    if (tag.startsWith('&')) {
      throw new TemplateError(state.location(token.offset), 'SYNTAX');
    }

    const expression = expressionText(tag, token.offset, state);
    countNode(state, token.offset);
    activeOutput.push({
      type: 'output',
      expression,
      location: state.location(token.offset),
    });
  }

  if (stack.length > 0) {
    throw new TemplateError(
      state.location(stack[stack.length - 1].openOffset),
      'SYNTAX',
    );
  }

  return { nodes: root, nodeCount: budget.nodes };
}

export function parseTemplateSource(
  source: string,
  limits: NormalizedLimits,
): TemplateAst {
  if (typeof source !== 'string') {
    throw new TemplateError(
      { offset: 0, line: 1, column: 1 },
      'SYNTAX',
    );
  }
  if (source.length > limits.maxTemplateLength) {
    const lineStarts = buildLineStarts(
      source.slice(0, limits.maxTemplateLength),
    );
    throw new TemplateError(
      locationAt(lineStarts, limits.maxTemplateLength),
      'BUDGET_EXCEEDED',
    );
  }

  const budget: ExpressionBudget = {
    maxNodes: limits.maxNodes,
    maxDepth: limits.maxDepth,
    lineStarts: buildLineStarts(source),
    nodes: 0,
  };

  let ast: TemplateAst;
  try {
    ast = parseTemplate(source, budget, limits.maxDepth);
  } catch (error) {
    if (error instanceof TemplateError) throw error;
    throw new TemplateError(
      { offset: 0, line: 1, column: 1 },
      'SYNTAX',
    );
  }
  // Expression parsing increments the shared node count. Re-count at most once
  // so callers can reason about a stable AST size without trusting parser
  // internals.
  let expressionNodes = 0;
  const visit = (nodes: TemplateNode[]): void => {
    for (const node of nodes) {
      if (node.type === 'text') continue;
      expressionNodes += countExpressionNodes(node.expression);
      if (node.type !== 'output') {
        visit(node.body);
        visit(node.alternate);
      }
    }
  };
  visit(ast.nodes);
  if (expressionNodes > limits.maxNodes) {
    throw new TemplateError(
      { offset: 0, line: 1, column: 1 },
      'BUDGET_EXCEEDED',
    );
  }

  return ast;
}
