// Minimal TypeScript type eraser for the disciplined "erasable syntax only"
// subset used in src/ and test/. Supports: interface/type/declare removal,
// import type removal, parameter/return/variable type annotations, optional
// parameter markers, ternaries, object literals, strings/templates/comments.
// Banned in sources: enums, namespaces, classes with modifiers, generics in
// value positions, `as` casts, non-null `!`, regex literals.

function isWs(ch) {
  return ch === " " || ch === "\t" || ch === "\r" || ch === "\n" || ch === "\f" || ch === "\v";
}

function isIdStart(ch) {
  if (!ch) return false;
  const code = ch.codePointAt(0);
  return ch === "$" || ch === "_" || (code >= 65 && code <= 90) || (code >= 97 && code <= 122) || code > 127;
}

function isIdChar(ch) {
  if (!ch) return false;
  return isIdStart(ch) || (ch >= "0" && ch <= "9");
}

function scanString(src, i) {
  const quote = src[i];
  let j = i + 1;
  while (j < src.length) {
    const ch = src[j];
    if (ch === "\\") { j += 2; continue; }
    if (ch === quote) return j + 1;
    j++;
  }
  return src.length;
}

function scanLineComment(src, i) {
  let j = i + 2;
  while (j < src.length && src[j] !== "\n") j++;
  return j;
}

function scanBlockComment(src, i) {
  let j = i + 2;
  while (j < src.length && !(src[j] === "*" && src[j + 1] === "/")) j++;
  return Math.min(src.length, j + 2);
}

function scanTemplate(src, i) {
  let j = i + 1;
  while (j < src.length) {
    const ch = src[j];
    if (ch === "\\") { j += 2; continue; }
    if (ch === "`") return j + 1;
    if (ch === "$" && src[j + 1] === "{") { j = scanTemplateExpr(src, j + 2); continue; }
    j++;
  }
  return src.length;
}

function scanTemplateExpr(src, i) {
  let depth = 1;
  let j = i;
  while (j < src.length && depth > 0) {
    const ch = src[j];
    if (ch === "{") { depth++; j++; continue; }
    if (ch === "}") { depth--; j++; continue; }
    if (ch === "'" || ch === '"') { j = scanString(src, j); continue; }
    if (ch === "`") { j = scanTemplate(src, j); continue; }
    if (ch === "/" && src[j + 1] === "/") { j = scanLineComment(src, j); continue; }
    if (ch === "/" && src[j + 1] === "*") { j = scanBlockComment(src, j); continue; }
    j++;
  }
  return j;
}

export function tokenize(src) {
  const tokens = [];
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (isWs(ch)) {
      let j = i + 1;
      while (j < src.length && isWs(src[j])) j++;
      tokens.push({ kind: "ws", start: i, end: j });
      i = j; continue;
    }
    if (ch === "/" && src[i + 1] === "/") {
      const j = scanLineComment(src, i);
      tokens.push({ kind: "comment", start: i, end: j });
      i = j; continue;
    }
    if (ch === "/" && src[i + 1] === "*") {
      const j = scanBlockComment(src, i);
      tokens.push({ kind: "comment", start: i, end: j });
      i = j; continue;
    }
    if (ch === "'" || ch === '"') {
      const j = scanString(src, i);
      tokens.push({ kind: "str", start: i, end: j });
      i = j; continue;
    }
    if (ch === "`") {
      const j = scanTemplate(src, i);
      tokens.push({ kind: "tpl", start: i, end: j });
      i = j; continue;
    }
    if (isIdStart(ch)) {
      let j = i + 1;
      while (j < src.length && isIdChar(src[j])) j++;
      tokens.push({ kind: "id", start: i, end: j });
      i = j; continue;
    }
    if (ch >= "0" && ch <= "9") {
      let j = i + 1;
      while (j < src.length && (isIdChar(src[j]) || src[j] === ".")) j++;
      tokens.push({ kind: "num", start: i, end: j });
      i = j; continue;
    }
    const three = src.slice(i, i + 3);
    const two = src.slice(i, i + 2);
    if (three === "..." || three === "??=") {
      tokens.push({ kind: "punct", start: i, end: i + 3 });
      i += 3; continue;
    }
    if (two === "=>" || two === "?." || two === "??") {
      tokens.push({ kind: "punct", start: i, end: i + 2 });
      i += 2; continue;
    }
    tokens.push({ kind: "punct", start: i, end: i + 1 });
    i += 1;
  }
  return tokens;
}

export function stripTypes(src) {
  const tokens = tokenize(src);
  const sig = [];
  for (const t of tokens) {
    if (t.kind !== "ws" && t.kind !== "comment") sig.push(t);
  }
  const text = (t) => src.slice(t.start, t.end);
  const ranges = [];
  const erased = new Array(sig.length).fill(false);

  const DECL_PREV = new Set([";", "{", "}", "export"]);

  // Pass 1: interface / type / declare / import type declarations.
  for (let k = 0; k < sig.length; k++) {
    if (erased[k]) continue;
    const t = sig[k];
    if (t.kind !== "id") continue;
    const word = text(t);
    if (word !== "interface" && word !== "type" && word !== "declare") continue;
    const prev = k > 0 ? sig[k - 1] : null;
    const prevText = prev ? text(prev) : null;
    const prevOk = prev === null || DECL_PREV.has(prevText) || (word === "type" && prevText === "import");
    if (!prevOk) continue;

    let endIdx = -1;
    if (word === "interface") {
      const name = sig[k + 1];
      const after = sig[k + 2];
      if (!name || name.kind !== "id" || !after) continue;
      const afterText = text(after);
      if (afterText !== "{" && afterText !== "extends") continue;
      let depth = 0;
      for (let m = k + 1; m < sig.length; m++) {
        const txt = text(sig[m]);
        if (txt === "{") depth++;
        else if (txt === "}") { depth--; if (depth === 0) { endIdx = m; break; } }
      }
      if (endIdx < 0) continue;
    } else {
      if (word === "type" && prevText !== "import") {
        const name = sig[k + 1];
        const after = sig[k + 2];
        if (!name || name.kind !== "id" || !after || text(after) !== "=") continue;
      }
      let depth = 0;
      for (let m = k + 1; m < sig.length; m++) {
        const txt = text(sig[m]);
        if (txt === "{" || txt === "(" || txt === "[" || txt === "<") depth++;
        else if (txt === "}" || txt === ")" || txt === "]" || txt === ">") {
          if (depth === 0) break;
          depth--;
        } else if (txt === ";" && depth === 0) { endIdx = m; break; }
      }
      if (endIdx < 0) continue;
    }
    const dropPrev = prevText === "export" || prevText === "import";
    const startPos = dropPrev ? prev.start : t.start;
    ranges.push([startPos, sig[endIdx].end]);
    for (let m = k; m <= endIdx; m++) erased[m] = true;
    if (dropPrev) erased[k - 1] = true;
  }

  const nextLive = (k) => {
    for (let m = k + 1; m < sig.length; m++) {
      if (!erased[m]) return m;
    }
    return -1;
  };
  const prevLive = (k) => {
    for (let m = k - 1; m >= 0; m--) {
      if (!erased[m]) return m;
    }
    return -1;
  };

  const eraseAnnotation = (colonIdx, stopTexts) => {
    let startPos = sig[colonIdx].start;
    const p = prevLive(colonIdx);
    if (p >= 0 && text(sig[p]) === "?" && sig[p].end === sig[colonIdx].start) {
      startPos = sig[p].start;
      erased[p] = true;
    }
    let depth = 0;
    let endPos = -1;
    let consumed = 0;
    for (let m = colonIdx + 1; m < sig.length; m++) {
      if (erased[m]) continue;
      const txt = text(sig[m]);
      if (depth === 0 && consumed > 0 && stopTexts.indexOf(txt) >= 0) { endPos = sig[m].start; break; }
      consumed++;
      if (txt === "(" || txt === "{" || txt === "[" || txt === "<") depth++;
      else if (txt === ")" || txt === "}" || txt === "]" || txt === ">") {
        if (depth === 0) { endPos = sig[m].start; break; }
        depth--;
      }
    }
    if (endPos < 0) {
      throw new Error("strip-types: unterminated type annotation near offset " + sig[colonIdx].start);
    }
    ranges.push([startPos, endPos]);
    for (let m = colonIdx; m < sig.length && sig[m].start < endPos; m++) erased[m] = true;
  };

  // Pass 2: type annotations.
  const stack = [];
  for (let k = 0; k < sig.length; k++) {
    if (erased[k]) continue;
    const t = sig[k];
    if (t.kind !== "punct") continue;
    const txt = text(t);
    if (txt === "(" || txt === "{" || txt === "[") { stack.push({ ch: txt, ternary: 0 }); continue; }
    if (txt === ")" || txt === "}" || txt === "]") { stack.pop(); continue; }
    if (txt === "?") {
      const nx = nextLive(k);
      if (nx >= 0 && text(sig[nx]) === ":") continue;
      if (stack.length > 0 && stack[stack.length - 1].ch === "(") stack[stack.length - 1].ternary++;
      continue;
    }
    if (txt !== ":") continue;
    const top = stack.length > 0 ? stack[stack.length - 1] : null;
    if (top && top.ch === "(") {
      if (top.ternary > 0) { top.ternary--; continue; }
      eraseAnnotation(k, [",", ")", "="]);
      continue;
    }
    const p = prevLive(k);
    if (p >= 0 && text(sig[p]) === ")") {
      eraseAnnotation(k, ["{", "=>"]);
      continue;
    }
    if (p >= 0 && sig[p].kind === "id") {
      const pp = prevLive(p);
      if (pp >= 0 && sig[pp].kind === "id") {
        const w = text(sig[pp]);
        if (w === "const" || w === "let" || w === "var") {
          eraseAnnotation(k, ["=", ";", ","]);
          continue;
        }
      }
    }
  }

  ranges.sort((a, b) => a[0] - b[0]);
  let out = "";
  let pos = 0;
  for (const r of ranges) {
    if (r[0] < pos) continue;
    out += src.slice(pos, r[0]);
    pos = r[1];
  }
  out += src.slice(pos);
  return out;
}
