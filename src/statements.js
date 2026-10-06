/**
 * Statement-level parser built on the tokenizer.
 *
 * Produces a flat list of statements at every nesting level:
 *   { start, end, kind, depth, parent, key }
 * - start/end: offsets spanning the statement's tokens (trivia excluded)
 * - kind: coarse classification from the leading keyword
 * - parent: index of the enclosing statement, or -1
 * - key: content key (kind + normalized token text) used by the identity model
 *
 * The parser is heuristic by design: it understands block structure,
 * semicolons and common ASI cases, not the full ECMAScript grammar.
 */

import { tokenize } from './tokenizer.js';

const DECL_KINDS = new Set(['const', 'let', 'var']);
const BLOCK_BODY_KINDS = new Set([
  'class', 'interface', 'namespace', 'module', 'enum', 'declare', 'type',
]);

const CONTINUATION_WORDS = new Set([
  'instanceof', 'in', 'of', 'extends', 'implements', 'else', 'catch', 'finally',
  'while', 'typeof', 'new', 'delete', 'void', 'yield', 'await', 'case',
]);

const TERMINATOR_WORDS = new Set(['return', 'throw', 'break', 'continue', 'yield', 'debugger']);

function isOperatorText(text) {
  return /^[=+\-*%&|^!~<>?:@#]/.test(text) && !/^[)\]};,]$/.test(text);
}

function prevSignificant(tokens, i) {
  for (let k = i - 1; k >= 0; k--) {
    if (tokens[k].type !== 'comment') return tokens[k];
  }
  return null;
}

function nextSignificant(tokens, i) {
  for (let k = i; k < tokens.length; k++) {
    if (tokens[k].type !== 'comment') return tokens[k];
  }
  return null;
}

function classifyKind(firstToken) {
  if (!firstToken) return 'empty';
  if (firstToken.type === 'word') {
    const w = firstToken.text;
    if (DECL_KINDS.has(w)) return 'var';
    if (w === 'function' || w === 'async') return 'function';
    if (w === 'if' || w === 'for' || w === 'while' || w === 'do' || w === 'switch' ||
        w === 'try' || w === 'class' || w === 'interface' || w === 'enum' ||
        w === 'namespace' || w === 'return' || w === 'throw' || w === 'import' ||
        w === 'export' || w === 'type' || w === 'declare' || w === 'abstract') {
      return w;
    }
    return 'expression';
  }
  if (firstToken.type === 'punct' && firstToken.text === '{') return 'block';
  if (firstToken.type === 'punct' && firstToken.text === ';') return 'empty';
  return 'expression';
}

export function parseStatements(src) {
  const tokens = tokenize(src);
  const statements = [];
  const childBlocks = []; // { openIdx, closeIdx, parentIndex, depth }

  // Parse a statement whose first significant token is at index `from`.
  // Returns the token index to continue the enclosing sequence from.
  function parseStatement(from, depth, parentIndex) {
    let first = from;
    while (first < tokens.length && tokens[first].type === 'comment') first++;
    if (first >= tokens.length) return tokens.length;

    const kind = classifyKind(tokens[first]);
    const stmtIndex = statements.length;
    const stmtTokens = [];
    const myBlocks = []; // { openIdx, closeIdx }

    let paren = 0;
    let bracket = 0;
    let brace = 0; // expression (object literal / type literal) brace depth
    let j = first;
    let done = false;

    const TYPE_PUNCTS = new Set([':', '|', '&', '<', '>', '[', ']', '.', '?', ',']);
    const isBlockBrace = (idx) => {
      if (paren !== 0 || bracket !== 0) return false;
      if (BLOCK_BODY_KINDS.has(kind) && brace === 0) return true;
      const prev = idx === first ? null : prevSignificant(tokens, idx);
      if (!prev) return true;
      if (prev.type === 'punct') {
        return prev.text === ')' || prev.text === '}' || prev.text === ';' ||
               prev.text === '{' || prev.text === '=>';
      }
      if (prev.type === 'word') {
        if (prev.text === 'else' || prev.text === 'try' ||
            prev.text === 'finally' || prev.text === 'do') return true;
        // A word directly before '{' may be a return-type annotation, as in
        // 'add(x): void {' or 'get first(): T {'. Scan back over the type
        // tail; a ':' before a ')' or the statement start means block body.
        let k = idx - 1;
        let sawColon = false;
        while (k >= first) {
          const t = tokens[k];
          if (t.type === 'comment') { k--; continue; }
          if (t.type === 'word') { k--; continue; }
          if (t.type === 'punct' && TYPE_PUNCTS.has(t.text)) {
            if (t.text === ':') sawColon = true;
            k--;
            continue;
          }
          break;
        }
        if (sawColon) {
          const before = k >= first ? tokens[k] : null;
          if (before === null) return true;
          if (before.type === 'punct' && before.text === ')') return true;
        }
      }
      return false;
    };

    while (j < tokens.length && !done) {
      const t = tokens[j];

      // A `}` closing an enclosing block ends this statement before it.
      if (t.type === 'punct' && t.text === '}' && paren === 0 && bracket === 0 && brace === 0) {
        break;
      }

      // ASI: a newline before this token may end the statement.
      if (t.newlinesBefore > 0 && paren === 0 && bracket === 0 && brace === 0 &&
          t.type !== 'comment' && j !== first &&
          !(t.type === 'punct' && t.text === '{')) {
        const prev = prevSignificant(tokens, j);
        const prevContinues = !!prev && (
          (prev.type === 'punct' && (prev.text === '.' || prev.text === ',' ||
            prev.text === '?.' || prev.text === '(' || prev.text === '[' ||
            prev.text === '=>' || isOperatorText(prev.text))) ||
          (prev.type === 'word' && CONTINUATION_WORDS.has(prev.text) &&
            !(prev.text === 'while' && kind === 'do'))
        );
        const nextContinues =
          (t.type === 'punct' && (t.text === '.' || t.text === ',' || t.text === '(' ||
            t.text === '[' || t.text === '?.' || isOperatorText(t.text))) ||
          t.type === 'template' ||
          (t.type === 'word' && CONTINUATION_WORDS.has(t.text) &&
            !(t.text === 'while' && kind === 'do'));
        const forcedEnd = !!prev && prev.type === 'word' && TERMINATOR_WORDS.has(prev.text);
        if ((!prevContinues && !nextContinues) || forcedEnd) break;
      }

      // Consume the token.
      if (t.type !== 'comment') stmtTokens.push(t);

      if (t.type === 'punct') {
        const txt = t.text;
        if (txt === '(') paren++;
        else if (txt === ')') paren = Math.max(0, paren - 1);
        else if (txt === '[') bracket++;
        else if (txt === ']') bracket = Math.max(0, bracket - 1);
        else if (txt === '{') {
          if (isBlockBrace(j)) {
            const nested = brace > 0; // e.g. a method body inside an object literal
            myBlocks.push({ openIdx: j });
            let inner = 1;
            j++;
            while (j < tokens.length && inner > 0) {
              const bt = tokens[j];
              if (bt.type === 'punct') {
                if (bt.text === '{') inner++;
                else if (bt.text === '}') inner--;
              }
              if (bt.type !== 'comment') stmtTokens.push(bt);
              j++;
            }
            myBlocks[myBlocks.length - 1].closeIdx = j - 1;
            if (nested) continue;
            const nxt = nextSignificant(tokens, j);
            if (nxt && nxt.type === 'word' &&
                (nxt.text === 'else' || nxt.text === 'catch' || nxt.text === 'finally' ||
                 (nxt.text === 'while' && kind === 'do'))) {
              continue; // else/catch/finally/do-while: statement continues
            }
            if (nxt && nxt.type === 'punct' && nxt.text === ';') {
              stmtTokens.push(nxt);
              j = tokens.indexOf(nxt, j) + 1;
            }
            done = true;
            continue;
          }
          brace++;
        } else if (txt === '}') {
          brace = Math.max(0, brace - 1);
        } else if (txt === ';' && paren === 0 && bracket === 0 && brace === 0) {
          done = true;
        }
      }
      j++;
    }

    if (stmtTokens.length === 0) return Math.max(j, from + 1);

    const start = stmtTokens[0].start;
    const end = stmtTokens[stmtTokens.length - 1].end;
    const key = kind + ':' + stmtTokens.map((t) => t.text).join(' ');

    statements.push({ start, end, kind, depth, parent: parentIndex, key });
    for (const blk of myBlocks) {
      if (blk.closeIdx !== undefined) {
        childBlocks.push({ openIdx: blk.openIdx, closeIdx: blk.closeIdx, parentIndex: stmtIndex, depth });
      }
    }
    return j;
  }

  // Parse a sequence of statements from token `from` until `untilIdx` (exclusive)
  // or a `}` token, whichever comes first.
  function parseSequence(from, untilIdx, depth, parentIndex) {
    let i = from;
    while (i < untilIdx) {
      const t = tokens[i];
      if (t.type === 'punct' && t.text === '}') return i;
      if (t.type === 'comment') { i++; continue; }
      const next = parseStatement(i, depth, parentIndex);
      i = Math.max(next, i + 1);
    }
    return i;
  }

  parseSequence(0, tokens.length, 0, -1);

  // Parse nested statements inside block braces, outermost first.
  const seen = new Set();
  const queue = [...childBlocks];
  while (queue.length > 0) {
    const blk = queue.shift();
    const tag = blk.openIdx + ':' + blk.closeIdx;
    if (seen.has(tag)) continue;
    seen.add(tag);
    parseSequence(blk.openIdx + 1, blk.closeIdx, blk.depth + 1, blk.parentIndex);
    for (const cand of childBlocks) {
      if (cand.openIdx > blk.openIdx && cand.closeIdx <= blk.closeIdx) {
        const ctag = cand.openIdx + ':' + cand.closeIdx;
        if (!seen.has(ctag)) queue.push(cand);
      }
    }
  }

  // Restore document order and remap parent indices.
  const order = statements.map((_, idx) => idx).sort((a, b) => statements[a].start - statements[b].start);
  const remap = new Map(order.map((oldIdx, newIdx) => [oldIdx, newIdx]));
  const sorted = order.map((oldIdx) => statements[oldIdx]);
  for (const s of sorted) {
    s.parent = s.parent === -1 ? -1 : remap.get(s.parent);
  }
  return sorted;
}

/** Find the innermost statement containing `offset` (smallest span wins). */
export function innermostStatementAt(statements, offset) {
  let best = -1;
  for (let i = 0; i < statements.length; i++) {
    const s = statements[i];
    if (s.start <= offset && offset <= s.end) {
      if (best === -1 || (s.end - s.start) < (statements[best].end - statements[best].start)) {
        best = i;
      }
    }
  }
  return best;
}
