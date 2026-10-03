// Wadler-style pretty-printing document algebra (the layout kernel).
// A Doc is rendered against a line-width budget: a `group` is printed flat
// when it fits on the remaining line, otherwise its `line`/`softline`
// joints become newlines at the current indentation level.

export type Doc =
  | { t: 'text'; s: string }
  | { t: 'concat'; parts: Doc[] }
  | { t: 'line' } // space when flat, newline when broken
  | { t: 'softline' } // nothing when flat, newline when broken
  | { t: 'group'; doc: Doc }
  | { t: 'indent'; doc: Doc };

export const text = (s: string): Doc => ({ t: 'text', s });

export const line: Doc = { t: 'line' };
export const softline: Doc = { t: 'softline' };

export const concat = (parts: Doc[]): Doc =>
  parts.length === 1 ? parts[0] : { t: 'concat', parts };

export const group = (...parts: Doc[]): Doc => ({ t: 'group', doc: concat(parts) });

export const indent = (...parts: Doc[]): Doc => ({ t: 'indent', doc: concat(parts) });

export const join = (sep: Doc, docs: Doc[]): Doc => {
  const out: Doc[] = [];
  docs.forEach((d, i) => {
    if (i > 0) out.push(sep);
    out.push(d);
  });
  return concat(out);
};

type Cmd = [indentLevel: number, flat: boolean, doc: Doc];

// Can `doc` fit within `remaining` columns when printed flat?
function fits(remaining: number, doc: Doc): boolean {
  let width = remaining;
  const stack: Doc[] = [doc];
  while (width >= 0 && stack.length > 0) {
    const d = stack.pop()!;
    switch (d.t) {
      case 'text':
        width -= d.s.length;
        break;
      case 'concat':
        for (let i = d.parts.length - 1; i >= 0; i--) stack.push(d.parts[i]);
        break;
      case 'group':
      case 'indent':
        stack.push(d.doc);
        break;
      case 'line':
        width -= 1;
        break;
      case 'softline':
        break;
    }
  }
  return width >= 0;
}

export function render(doc: Doc, width = 80, indentSize = 2): string {
  const cmds: Cmd[] = [[0, false, doc]];
  let out = '';
  let col = 0;
  while (cmds.length > 0) {
    const [ind, flat, d] = cmds.pop()!;
    switch (d.t) {
      case 'text':
        out += d.s;
        col += d.s.length;
        break;
      case 'concat':
        for (let i = d.parts.length - 1; i >= 0; i--) {
          cmds.push([ind, flat, d.parts[i]]);
        }
        break;
      case 'indent':
        cmds.push([ind + indentSize, flat, d.doc]);
        break;
      case 'group':
        cmds.push([ind, fits(width - col, d.doc), d.doc]);
        break;
      case 'line':
        if (flat) {
          out += ' ';
          col += 1;
        } else {
          out += '\n' + ' '.repeat(ind);
          col = ind;
        }
        break;
      case 'softline':
        if (!flat) {
          out += '\n' + ' '.repeat(ind);
          col = ind;
        }
        break;
    }
  }
  return out;
}
