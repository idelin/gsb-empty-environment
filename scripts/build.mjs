import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const modules = ["types", "subst", "unify", "index"];

const MULTI = ["===", "!==", "...", "=>", "?.", "??", "&&", "||", "==", "!=", "<=", ">=", "++", "--", "+=", "-=", "*=", "/=", "**"];

function tokenize(src) {
  const tokens = [];
  let i = 0;
  const n = src.length;
  const push = (kind, start, end) => tokens.push({ kind, text: src.slice(start, end), start, end });
  while (i < n) {
    const ch = src[i];
    const start = i;
    if (ch === " " || ch === "\t" || ch === "\r" || ch === "\n") {
      while (i < n && (src[i] === " " || src[i] === "\t" || src[i] === "\r" || src[i] === "\n")) i++;
      push("ws", start, i);
    } else if (ch === "/" && src[i + 1] === "/") {
      while (i < n && src[i] !== "\n") i++;
      push("comment", start, i);
    } else if (ch === "/" && src[i + 1] === "*") {
      const close = src.indexOf("*/", i + 2);
      if (close === -1) throw new Error("unterminated block comment");
      i = close + 2;
      push("comment", start, i);
    } else if (ch === "'" || ch === '"' || ch === "`") {
      i++;
      while (i < n) {
        if (src[i] === "\\") { i += 2; continue; }
        if (src[i] === ch) { i++; break; }
        if (ch === "`" && src[i] === "$" && src[i + 1] === "{") {
          let depth = 1;
          i += 2;
          while (i < n && depth > 0) {
            if (src[i] === "{") depth++;
            else if (src[i] === "}") depth--;
            else if (src[i] === "'" || src[i] === '"' || src[i] === "`") {
              const q = src[i];
              i++;
              while (i < n && src[i] !== q) { if (src[i] === "\\") i++; i++; }
            }
            i++;
          }
          continue;
        }
        i++;
      }
      push("string", start, i);
    } else if (/[0-9]/.test(ch)) {
      while (i < n && /[0-9a-fA-FxXoObB._]/.test(src[i])) i++;
      push("number", start, i);
    } else if (/[A-Za-z_$]/.test(ch)) {
      while (i < n && /[0-9A-Za-z_$]/.test(src[i])) i++;
      push("id", start, i);
    } else {
      let matched = null;
      for (const m of MULTI) {
        if (src.startsWith(m, i)) { matched = m; break; }
      }
      if (matched !== null) i += matched.length;
      else i++;
      push("punct", start, i);
    }
  }
  return tokens;
}

function stripTypes(src) {
  const toks = tokenize(src).filter((t) => t.kind !== "ws" && t.kind !== "comment");
  const removed = new Array(toks.length).fill(false);

  function removeRange(from, to) {
    for (let k = from; k <= to; k++) removed[k] = true;
  }

  function matchBrace(start) {
    let depth = 0;
    for (let j = start; j < toks.length; j++) {
      if (toks[j].text === "{") depth++;
      else if (toks[j].text === "}") {
        depth--;
        if (depth === 0) return j;
      }
    }
    throw new Error("unbalanced braces");
  }

  function skipAngles(start) {
    let depth = 0;
    for (let j = start; j < toks.length; j++) {
      if (toks[j].text === "<") depth++;
      else if (toks[j].text === ">") {
        depth--;
        if (depth === 0) return j;
      }
    }
    throw new Error("unbalanced angle brackets");
  }

  function nearestOpener(i) {
    const stack = [];
    for (let k = 0; k < i; k++) {
      const t = toks[k].text;
      if (t === "(" || t === "{" || t === "[") stack.push(t);
      else if (t === ")" || t === "}" || t === "]") stack.pop();
    }
    return stack.length === 0 ? null : stack[stack.length - 1];
  }

  for (let i = 0; i < toks.length; i++) {
    if (removed[i]) continue;
    const t = toks[i];

    if (t.kind === "id" && t.text === "interface" && i + 2 < toks.length && toks[i + 1].kind === "id") {
      const after = toks[i + 2].text;
      if (after === "{" || after === "<" || after === "extends") {
        let brace = i + 2;
        if (after === "<") brace = skipAngles(i + 2) + 1;
        while (toks[brace].text !== "{") brace++;
        const end = matchBrace(brace);
        const start = i > 0 && toks[i - 1].text === "export" ? i - 1 : i;
        removeRange(start, end);
        continue;
      }
    }

    if (t.kind === "id" && t.text === "type" && i + 2 < toks.length && toks[i + 1].kind === "id") {
      const after = toks[i + 2].text;
      if (after === "=" || after === "<") {
        let j = i + 2;
        let depth = 0;
        while (j < toks.length) {
          const tx = toks[j].text;
          if (tx === "{" || tx === "(" || tx === "[" || tx === "<") depth++;
          else if (tx === "}" || tx === ")" || tx === "]" || tx === ">") depth--;
          else if (tx === ";" && depth === 0) break;
          j++;
        }
        const start = i > 0 && toks[i - 1].text === "export" ? i - 1 : i;
        removeRange(start, j);
        continue;
      }
    }

    if (t.kind === "id" && t.text === "import" && i + 1 < toks.length && toks[i + 1].text === "type") {
      let j = i + 1;
      let depth = 0;
      while (j < toks.length) {
        const tx = toks[j].text;
        if (tx === "{" || tx === "(" || tx === "[") depth++;
        else if (tx === "}" || tx === ")" || tx === "]") depth--;
        else if (tx === ";" && depth === 0) break;
        j++;
      }
      removeRange(i, j);
      continue;
    }

    if (t.text === ":") {
      const prev = i > 0 ? toks[i - 1] : null;
      let isAnnotation = false;
      if (prev && prev.text === ")") isAnnotation = true;
      else if (nearestOpener(i) === "(") isAnnotation = true;
      else if (prev && prev.kind === "id" && i > 1 && ["const", "let", "var"].includes(toks[i - 2].text)) isAnnotation = true;
      if (isAnnotation) {
        let j = i + 1;
        let depth = 0;
        while (j < toks.length) {
          const tx = toks[j].text;
          if (tx === "{" || tx === "(" || tx === "[" || tx === "<") {
            if (depth === 0 && tx === "{") break;
            depth++;
          } else if (tx === "}" || tx === ")" || tx === "]" || tx === ">") {
            if (depth === 0) break;
            depth--;
          } else if (depth === 0 && (tx === "," || tx === "=" || tx === ";" || tx === "=>")) {
            break;
          }
          j++;
        }
        removeRange(i, j - 1);
        continue;
      }
    }

    if (t.kind === "id" && i + 1 < toks.length && toks[i + 1].text === "<") {
      const end = skipAngles(i + 1);
      removeRange(i + 1, end);
      continue;
    }
  }

  let out = "";
  let cursor = 0;
  for (let i = 0; i < toks.length; i++) {
    const tok = toks[i];
    if (removed[i]) {
      cursor = tok.end;
      continue;
    }
    out += src.slice(cursor, tok.start);
    out += tok.text;
    cursor = tok.end;
  }
  out += src.slice(cursor);
  return out;
}

const srcDir = join(root, "src");
const distDir = join(root, "dist");
mkdirSync(distDir, { recursive: true });
for (const name of modules) {
  const src = readFileSync(join(srcDir, name + ".ts"), "utf8");
  const js = stripTypes(src);
  writeFileSync(join(distDir, name + ".js"), js);
  console.log("built dist/" + name + ".js");
}
