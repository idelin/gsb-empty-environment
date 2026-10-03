import type { JsonValue, PathSeg } from "./json.ts";

/**
 * Template expressions (inside ${...} or in directive fields):
 *   path:    user.name  items[0].id  @it.name
 *   call:    upper(user.name)  join(tags, ', ')
 *   literal: 42  -1.5  'str'  "str"  true  false  null
 */
export type Expr =
  | { t: "lit"; v: JsonValue }
  | { t: "path"; segs: PathSeg[] } // first segment may be "@var"
  | { t: "call"; fn: string; args: Expr[] };

export class ParseError extends Error {}

const IDENT = /[A-Za-z0-9_$]/;

export function parseExpr(src: string): Expr {
  const s = src;
  let i = 0;

  function err(msg: string): ParseError {
    return new ParseError(`expression parse error at offset ${i} in ${JSON.stringify(src)}: ${msg}`);
  }
  function skip(): void {
    while (i < s.length && /\s/.test(s[i])) i++;
  }
  function parseString(quote: string): string {
    let out = "";
    i++; // opening quote
    while (i < s.length) {
      const c = s[i];
      if (c === "\\" && i + 1 < s.length) {
        const n = s[i + 1];
        if (n === "n") out += "\n";
        else if (n === "t") out += "\t";
        else out += n;
        i += 2;
        continue;
      }
      if (c === quote) {
        i++;
        return out;
      }
      out += c;
      i++;
    }
    throw err("unterminated string literal");
  }
  function parsePrimary(): Expr {
    skip();
    if (i >= s.length) throw err("unexpected end of expression");
    const ch = s[i];
    if (ch === "'" || ch === '"') return { t: "lit", v: parseString(ch) };
    if (ch === "-" || /\d/.test(ch)) {
      const m = /^-?\d+(\.\d+)?/.exec(s.slice(i));
      if (!m) throw err("bad number");
      i += m[0].length;
      return { t: "lit", v: Number(m[0]) };
    }
    if (ch === "(") {
      i++;
      const e = parsePrimary();
      skip();
      if (s[i] !== ")") throw err("expected ')'");
      i++;
      return e;
    }
    if (ch === "@" || /[A-Za-z_$]/.test(ch)) {
      let name = "";
      if (ch === "@") {
        name = "@";
        i++;
      }
      while (i < s.length && IDENT.test(s[i])) {
        name += s[i];
        i++;
      }
      if (name === "" || name === "@") throw err("expected identifier");
      if (!name.startsWith("@")) {
        if (name === "true") return { t: "lit", v: true };
        if (name === "false") return { t: "lit", v: false };
        if (name === "null") return { t: "lit", v: null };
      }
      skip();
      if (s[i] === "(" && !name.startsWith("@")) {
        i++;
        const args: Expr[] = [];
        skip();
        if (s[i] !== ")") {
          for (;;) {
            args.push(parsePrimary());
            skip();
            if (s[i] === ",") {
              i++;
              continue;
            }
            break;
          }
        }
        skip();
        if (s[i] !== ")") throw err("expected ')'");
        i++;
        return { t: "call", fn: name, args };
      }
      const segs: PathSeg[] = [name];
      for (;;) {
        skip();
        if (s[i] === ".") {
          i++;
          skip();
          let seg = "";
          while (i < s.length && IDENT.test(s[i])) {
            seg += s[i];
            i++;
          }
          if (!seg) throw err("expected property name after '.'");
          segs.push(seg);
        } else if (s[i] === "[") {
          i++;
          skip();
          const m = /^\d+/.exec(s.slice(i));
          if (!m) throw err("expected array index");
          segs.push(Number(m[0]));
          i += m[0].length;
          skip();
          if (s[i] !== "]") throw err("expected ']'");
          i++;
        } else {
          break;
        }
      }
      return { t: "path", segs };
    }
    throw err(`unexpected character '${ch}'`);
  }

  const e = parsePrimary();
  skip();
  if (i < s.length) throw err("trailing characters");
  return e;
}
