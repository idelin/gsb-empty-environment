import { FD, Schema } from './model';

export type Symbol =
  | { kind: 'distinguished'; col: number }
  | { kind: 'nondistinguished'; row: number; col: number };

export interface MergeStep {
  step: number;
  fdIndex: number;
  rows: [number, number];
  column: number;
  lhsSymbols: Symbol[];
  absorbed: Symbol;
  into: Symbol;
}

export interface ChaseResult {
  symbols: Symbol[];
  cellSymbol: number[][];
  finalRoots: number[][];
  steps: MergeStep[];
  witnessRow: number | null;
  witnessFirstStep: number | null;
  lossless: boolean;
}

function symbolRank(symbol: Symbol): [number, number, number] {
  return symbol.kind === 'distinguished'
    ? [0, symbol.col, 0]
    : [1, symbol.row, symbol.col];
}

function compareRank(a: Symbol, b: Symbol): number {
  const ra = symbolRank(a);
  const rb = symbolRank(b);
  for (let k = 0; k < 3; k++) {
    if (ra[k] !== rb[k]) {
      return ra[k] - rb[k];
    }
  }
  return 0;
}

export function symbolName(symbol: Symbol): string {
  return symbol.kind === 'distinguished'
    ? `a${symbol.col + 1}`
    : `b${symbol.row + 1},${symbol.col + 1}`;
}

class DisjointSet {
  private parent: number[];

  constructor(size: number) {
    this.parent = new Array<number>(size);
    for (let i = 0; i < size; i++) {
      this.parent[i] = i;
    }
  }

  find(x: number): number {
    let root = x;
    while (this.parent[root] !== root) {
      root = this.parent[root];
    }
    let cur = x;
    while (this.parent[cur] !== root) {
      const next = this.parent[cur];
      this.parent[cur] = root;
      cur = next;
    }
    return root;
  }

  union(a: number, b: number, symbols: Symbol[]): { merged: boolean; root: number; absorbed: number } {
    const ra = this.find(a);
    const rb = this.find(b);
    if (ra === rb) {
      return { merged: false, root: ra, absorbed: ra };
    }
    const cmp = compareRank(symbols[ra], symbols[rb]);
    const root = cmp <= 0 ? ra : rb;
    const absorbed = cmp <= 0 ? rb : ra;
    this.parent[absorbed] = root;
    return { merged: true, root, absorbed };
  }
}

export function runChase(schema: Schema): ChaseResult {
  const numRows = schema.decomposition.length;
  const numCols = schema.attributes.length;

  const symbols: Symbol[] = [];
  for (let col = 0; col < numCols; col++) {
    symbols.push({ kind: 'distinguished', col });
  }
  const cellSymbol: number[][] = [];
  for (let row = 0; row < numRows; row++) {
    const member = new Set<number>(schema.decomposition[row]);
    const cells: number[] = [];
    for (let col = 0; col < numCols; col++) {
      if (member.has(col)) {
        cells.push(col);
      } else {
        symbols.push({ kind: 'nondistinguished', row, col });
        cells.push(symbols.length - 1);
      }
    }
    cellSymbol.push(cells);
  }

  const ds = new DisjointSet(symbols.length);
  const steps: MergeStep[] = [];
  let witnessFirstStep: number | null = null;

  const hasDistinguishedRow = (): boolean => {
    for (let row = 0; row < numRows; row++) {
      let all = true;
      for (let col = 0; col < numCols; col++) {
        if (symbols[ds.find(cellSymbol[row][col])].kind !== 'distinguished') {
          all = false;
          break;
        }
      }
      if (all) {
        return true;
      }
    }
    return false;
  };

  const applyFd = (fdIndex: number, fd: FD): boolean => {
    let changed = false;
    for (let i = 0; i < numRows; i++) {
      for (let j = i + 1; j < numRows; j++) {
        let agree = true;
        for (const col of fd.lhs) {
          if (ds.find(cellSymbol[i][col]) !== ds.find(cellSymbol[j][col])) {
            agree = false;
            break;
          }
        }
        if (!agree) {
          continue;
        }
        const lhsSymbols = fd.lhs.map((col) => symbols[ds.find(cellSymbol[i][col])]);
        for (const col of fd.rhs) {
          const outcome = ds.union(cellSymbol[i][col], cellSymbol[j][col], symbols);
          if (outcome.merged) {
            steps.push({
              step: steps.length + 1,
              fdIndex,
              rows: [i, j],
              column: col,
              lhsSymbols,
              absorbed: symbols[outcome.absorbed],
              into: symbols[outcome.root],
            });
            changed = true;
            if (witnessFirstStep === null && hasDistinguishedRow()) {
              witnessFirstStep = steps.length;
            }
          }
        }
      }
    }
    return changed;
  };

  let changed = true;
  while (changed) {
    changed = false;
    for (let fdIndex = 0; fdIndex < schema.fds.length; fdIndex++) {
      if (applyFd(fdIndex, schema.fds[fdIndex])) {
        changed = true;
      }
    }
  }

  const finalRoots: number[][] = cellSymbol.map((row) => row.map((id) => ds.find(id)));

  let witnessRow: number | null = null;
  for (let row = 0; row < numRows; row++) {
    const all = finalRoots[row].every((root) => symbols[root].kind === 'distinguished');
    if (all) {
      witnessRow = row;
      break;
    }
  }

  return {
    symbols,
    cellSymbol,
    finalRoots,
    steps,
    witnessRow,
    witnessFirstStep,
    lossless: witnessRow !== null,
  };
}

export function renderTableau(schema: Schema, symbols: Symbol[], roots: number[][]): string[] {
  const header = ['行'].concat(schema.attributes.map((name) => name));
  const body = roots.map((row, rowIdx) =>
    [`r${rowIdx + 1}`].concat(row.map((root) => symbolName(symbols[root]))),
  );
  const widths = header.map((_, colIdx) =>
    Math.max(header[colIdx].length, ...body.map((row) => row[colIdx].length)),
  );
  const fmt = (cells: string[]): string =>
    cells.map((cell, colIdx) => cell.padEnd(widths[colIdx])).join('  ').trimEnd();
  return [fmt(header), ...body.map(fmt)];
}
