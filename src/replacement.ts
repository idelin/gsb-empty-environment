/**
 * Replacement-template expansion following JavaScript `String.replace`
 * dollar-sign semantics:
 *   `$$`        literal `$` (the escape sequence)
 *   `$&`        the whole match
 *   `$``        portion of the source before the match
 *   `$'`        portion of the source after the match
 *   `$n`/`$nn`  capture group n (1..99); a non-participating group expands
 *               to the empty string; an out-of-range `$n` stays literal
 *   `$<name>`   named capture group; literal when the name does not exist
 */
export function expandReplacement(
  template: string,
  match: RegExpExecArray,
  source: string,
): string {
  const matchStart = match.index;
  const matchText = match[0];
  const groupCount = match.length - 1;
  const groups = match.groups;
  let out = '';
  let i = 0;
  while (i < template.length) {
    const ch = template.charAt(i);
    if (ch !== '$' || i + 1 >= template.length) {
      out += ch;
      i += 1;
      continue;
    }
    const next = template.charAt(i + 1);
    switch (next) {
      case '$':
        out += '$';
        i += 2;
        continue;
      case '&':
        out += matchText;
        i += 2;
        continue;
      case '`':
        out += source.slice(0, matchStart);
        i += 2;
        continue;
      case "'":
        out += source.slice(matchStart + matchText.length);
        i += 2;
        continue;
      case '<': {
        const close = template.indexOf('>', i + 2);
        if (close === -1) {
          out += ch;
          i += 1;
          continue;
        }
        const name = template.slice(i + 2, close);
        if (groups !== undefined && Object.prototype.hasOwnProperty.call(groups, name)) {
          const value = groups[name];
          out += value === undefined ? '' : value;
          i = close + 1;
        } else {
          out += ch;
          i += 1;
        }
        continue;
      }
      default: {
        if (next >= '0' && next <= '9') {
          const two = template.slice(i + 1, i + 3);
          const one = template.slice(i + 1, i + 2);
          const twoNum = two.length === 2 && /^\d\d$/.test(two) ? parseInt(two, 10) : NaN;
          const oneNum = parseInt(one, 10);
          let ref = 0;
          let consumed = 0;
          if (!Number.isNaN(twoNum) && twoNum >= 1 && twoNum <= groupCount) {
            ref = twoNum;
            consumed = 3;
          } else if (oneNum >= 1 && oneNum <= groupCount) {
            ref = oneNum;
            consumed = 2;
          }
          if (consumed > 0) {
            const value = match[ref];
            out += value === undefined ? '' : value;
            i += consumed;
          } else {
            out += ch;
            i += 1;
          }
        } else {
          out += ch;
          i += 1;
        }
      }
    }
  }
  return out;
}
