// visual.ts — tab-stop math.
// All columns are *visual* columns: a tab advances to the next multiple of
// tabSize, every other character advances by exactly one column.

const TAB_SIZE = 4;

// Visual width of a string prefix (may contain tabs).
function visualWidth(prefix: string, tabSize: number): number {
  let col = 0;
  for (let i = 0; i < prefix.length; i++) {
    if (prefix.charCodeAt(i) === 9) {
      col += tabSize - (col % tabSize);
    } else {
      col += 1;
    }
  }
  return col;
}

// Smallest tab stop strictly greater than col (a full step when col is
// already exactly on a stop).
function nextTabStop(col: number, tabSize: number): number {
  return col + (tabSize - (col % tabSize));
}

// Visual width of the leading whitespace run of a line.
function indentWidth(line: string, tabSize: number): number {
  let col = 0;
  for (let i = 0; i < line.length; i++) {
    const code = line.charCodeAt(i);
    if (code === 9) {
      col += tabSize - (col % tabSize);
    } else if (code === 32) {
      col += 1;
    } else {
      break;
    }
  }
  return col;
}

// Number of leading whitespace characters that outdent may remove: the
// longest prefix of the whitespace run whose cumulative visual width is
// at most one tab stop. Existing tabs are measured by visual column.
function outdentCharCount(line: string, tabSize: number): number {
  let col = 0;
  let count = 0;
  for (let i = 0; i < line.length; i++) {
    const code = line.charCodeAt(i);
    let w = 0;
    if (code === 9) {
      w = tabSize - (col % tabSize);
    } else if (code === 32) {
      w = 1;
    } else {
      break;
    }
    if (col + w > tabSize) {
      break;
    }
    col += w;
    count = i + 1;
  }
  return count;
}

export { TAB_SIZE, visualWidth, nextTabStop, indentWidth, outdentCharCount };
