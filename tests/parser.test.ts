import { describe, expect, test } from 'bun:test';
import { visualColumn } from '../src/indent';
import { parse } from '../src/parser';
import { reprint, findPath, type RootNode } from '../src/cst';
import { referenceModel, flatten, crossCheck } from '../src/verify';

describe('visual column (8-column tab stops)', () => {
  test('tabs jump to the next multiple of 8', () => {
    expect(visualColumn('')).toBe(0);
    expect(visualColumn('\t')).toBe(8);
    expect(visualColumn('  \t')).toBe(8);
    expect(visualColumn('        ')).toBe(8);
    expect(visualColumn(' \t')).toBe(8);
    expect(visualColumn('\t ')).toBe(9);
    expect(visualColumn('       \t')).toBe(8);
    expect(visualColumn('\t\t')).toBe(16);
  });
});

describe('hierarchy', () => {
  const src = 'a: 1\nb:\n  c: 2\n  d:\n    e: 3\nf: 4\n';

  test('parser tree matches the explicit-stack reference model', () => {
    const { root, errors } = parse(src);
    expect(errors).toEqual([]);
    const model = referenceModel(src);
    expect(flatten(src, root)).toEqual(model.entries);
    expect(model.entries).toEqual([
      { line: 1, depth: 0, key: 'a', value: '1' },
      { line: 2, depth: 0, key: 'b', value: null },
      { line: 3, depth: 1, key: 'c', value: '2' },
      { line: 4, depth: 1, key: 'd', value: null },
      { line: 5, depth: 2, key: 'e', value: '3' },
      { line: 6, depth: 0, key: 'f', value: '4' },
    ]);
  });

  test('blank lines and comments do not affect hierarchy', () => {
    const commented = '# head\na: 1\n\nb:\n  # inside\n  c: 2\n\n\nf: 4\n';
    const { root, errors } = parse(commented);
    expect(errors).toEqual([]);
    expect(flatten(commented, root)).toEqual([
      { line: 2, depth: 0, key: 'a', value: '1' },
      { line: 4, depth: 0, key: 'b', value: null },
      { line: 6, depth: 1, key: 'c', value: '2' },
      { line: 9, depth: 0, key: 'f', value: '4' },
    ]);
  });

  test('mixed tabs and spaces form consistent levels when prefixes match', () => {
    const mixed = 'a:\n\tb: 1\n\tc:\n\t\td: 2\n';
    const { root, errors } = parse(mixed);
    expect(errors).toEqual([]);
    expect(flatten(mixed, root)).toEqual(referenceModel(mixed).entries);
    const c = findPath(root, ['a', 'c']);
    expect(c?.kind).toBe('block');
  });
});

describe('concrete syntax tree fidelity', () => {
  test('reprint reproduces the source byte for byte', () => {
    const src = '# c1\na: 1\n\nb:\n  # c2\n  c: two words  \n\t\nf: 4\n';
    const { root, errors } = parse(src);
    expect(errors).toEqual([]);
    expect(reprint(src, root)).toBe(src);
  });

  test('original CRLF newlines are preserved', () => {
    const src = 'a: 1\r\nb:\r\n  c: 2\r\n';
    const { root, errors } = parse(src);
    expect(errors).toEqual([]);
    expect(reprint(src, root)).toBe(src);
  });

  test('reprint identity holds even when errors were recovered', () => {
    const src = 'a:\n  b: 1\n\tx: bad\n  y: skipped\nc: 3\n';
    const { root, errors } = parse(src);
    expect(errors.length).toBe(1);
    expect(reprint(src, root)).toBe(src);
  });
});

describe('indentation errors', () => {
  test('same visual column, different characters => ambiguity with line/col', () => {
    const src = 'a:\n        b: 1\n\tc: 2\n';
    const { errors } = parse(src);
    expect(errors.length).toBe(1);
    expect(errors[0].code).toBe('ambiguous-indent');
    expect(errors[0].line).toBe(3);
    expect(errors[0].col).toBe(1); // first differing char: ' ' vs '\t'
  });

  test('space prefix vs tab prefix established earlier, reversed order', () => {
    const src = 'a:\n\tb: 1\n        c: 2\n';
    const { errors } = parse(src);
    expect(errors.length).toBe(1);
    expect(errors[0].code).toBe('ambiguous-indent');
    expect(errors[0].line).toBe(3);
  });

  test('dedent must match an old level', () => {
    const src = 'a:\n    b: 1\n  c: 2\n';
    const { errors } = parse(src);
    expect(errors.length).toBe(1);
    expect(errors[0].code).toBe('bad-dedent');
    expect(errors[0].line).toBe(3);
  });

  test('indent after a leaf is unexpected', () => {
    const src = 'a: 1\n  b: 2\n';
    const { errors } = parse(src);
    expect(errors.length).toBe(1);
    expect(errors[0].code).toBe('unexpected-indent');
    expect(errors[0].line).toBe(2);
  });

  test('line without a colon is a bad line', () => {
    const { errors } = parse('a: 1\noops\n');
    expect(errors.length).toBe(1);
    expect(errors[0].code).toBe('bad-line');
    expect(errors[0].line).toBe(2);
  });
});

describe('error recovery to the next top level', () => {
  const src = 'a:\n  b: 1\n\tx: bad\n  y: skipped\n    z: skipped\nc: 3\nd:\n  e: 4\n';

  test('parsing resumes at the next top-level line', () => {
    const { root, errors } = parse(src);
    expect(errors.length).toBe(1);
    expect(errors[0].line).toBe(3);
    expect(flatten(src, root)).toEqual([
      { line: 1, depth: 0, key: 'a', value: null },
      { line: 2, depth: 1, key: 'b', value: '1' },
      { line: 6, depth: 0, key: 'c', value: '3' },
      { line: 7, depth: 0, key: 'd', value: null },
      { line: 8, depth: 1, key: 'e', value: '4' },
    ]);
  });

  test('reference model agrees on entries and error lines', () => {
    const { model, actual, errors } = crossCheck(src);
    expect(actual).toEqual(model.entries);
    expect(errors).toEqual(model.errorLines);
  });
});

describe('randomized cross-check against the reference model', () => {
  function mulberry32(seed: number) {
    return () => {
      seed |= 0;
      seed = (seed + 0x6d2b79f5) | 0;
      let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  function generate(rand: () => number): string {
    const lines: string[] = [];
    const levels: Array<{ col: number; prefix: string }> = [{ col: 0, prefix: '' }];
    let prevHeader = false;
    let key = 0;
    const randPrefix = (col: number): string => {
      const chars: string[] = [];
      let c = 0;
      while (c < col) {
        if (col - c >= 8 && rand() < 0.4) {
          chars.push('\t');
          c += 8 - (c % 8);
        } else {
          chars.push(' ');
          c += 1;
        }
      }
      return chars.join('');
    };
    for (let n = 0; n < 40; n++) {
      const r = rand();
      if (r < 0.15) {
        lines.push(rand() < 0.5 ? '' : '# comment ' + n);
        continue;
      }
      // pick a target depth: sometimes valid, sometimes not
      let depth = Math.floor(rand() * (levels.length + 1));
      let prefix: string;
      let header = rand() < 0.3;
      if (depth === levels.length && prevHeader) {
        const col = levels[levels.length - 1].col + 2 * (1 + Math.floor(rand() * 3));
        prefix = randPrefix(col);
        levels.push({ col, prefix });
      } else {
        depth = Math.min(depth, levels.length - 1);
        prefix = levels[depth].prefix;
        if (rand() < 0.15) prefix = randPrefix(levels[depth].col); // maybe corrupt
        levels.length = depth + 1;
      }
      const name = 'k' + key++;
      lines.push(prefix + (rand() < 0.05 ? name : name + (header ? ':' : ': v' + key)));
      prevHeader = header;
    }
    return lines.join('\n') + '\n';
  }

  for (let seed = 1; seed <= 30; seed++) {
    test(`seed ${seed}: parser and model agree`, () => {
      const src = generate(mulberry32(seed));
      const { model, actual, errors } = crossCheck(src);
      expect(actual).toEqual(model.entries);
      expect(errors).toEqual(model.errorLines);
      const { root } = parse(src);
      expect(reprint(src, root)).toBe(src);
    });
  }
});
