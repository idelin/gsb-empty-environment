import { DimensionTree } from './dimensionTree.js';
import type { DimNode } from './dimensionTree.js';
import { createMeasureState } from './aggregators.js';
import type { MeasureState } from './aggregators.js';
import { stableSort, compareLabels } from './stableSort.js';
import type {
  DataRecord,
  MeasureMeta,
  PivotColumn,
  PivotConfig,
  PivotResult,
  PivotRow,
  SortComparator,
  SortSpec,
} from './types.js';

interface DebugStats {
  records: number;
  rowNodes: number;
  colNodes: number;
  aggNodes: number;
}

interface StoredRecord {
  id: string | number;
  rowLabels: string[];
  colLabels: string[];
  values: (number | null)[];
}

interface CellEntry {
  states: MeasureState[];
  count: number;
}

interface RowLayout {
  node: DimNode;
  displayPath: (string | null)[];
  path: string[];
  isSubtotal: boolean;
  isGrandTotal: boolean;
  level: number;
}

interface ColumnLayout {
  path: string[];
  isSubtotal: boolean;
  isGrandTotal: boolean;
  level: number;
  leafKeys: string[];
}

export class PivotTable {
  private readonly rowFields: string[];
  private readonly colFields: string[];
  private readonly measures: MeasureMeta[];
  private readonly blankLabel: string;
  private readonly subtotalLabel: string;
  private readonly grandTotalLabel: string;
  private readonly sorts: { [field: string]: SortSpec };
  private readonly rowTree = new DimensionTree();
  private readonly colTree = new DimensionTree();
  private readonly records = new Map<string | number, StoredRecord>();
  private readonly aggs = new Map<DimNode, Map<string, CellEntry>>();
  private autoId = 0;

  constructor(config: PivotConfig) {
    this.rowFields = config.rows.slice();
    this.colFields = config.columns.slice();
    this.measures = config.measures.map((m) => ({
      field: m.field,
      aggregation: m.aggregation,
      label: m.label ?? m.field,
    }));
    this.blankLabel = config.blankLabel ?? '(空)';
    this.subtotalLabel = config.subtotalLabel ?? '小计';
    this.grandTotalLabel = config.grandTotalLabel ?? '总计';
    this.sorts = config.sorts ?? {};
  }

  get size(): number {
    return this.records.size;
  }

  addRecord(record: DataRecord): string | number {
    let id = record.id;
    if (id === undefined || id === null) {
      this.autoId += 1;
      id = '__auto_' + String(this.autoId);
    }
    if (this.records.has(id)) {
      throw new Error('Duplicate record id: ' + String(id));
    }
    const rowLabels = this.rowFields.map((field) => this.normalize(record[field]));
    const colLabels = this.colFields.map((field) => this.normalize(record[field]));
    const values = this.measures.map((m) => PivotTable.toNumber(record[m.field]));
    const rowNodes = this.rowTree.addPath(rowLabels);
    this.colTree.addPath(colLabels);
    const colLeafKey = JSON.stringify(colLabels);
    for (const node of rowNodes) {
      let cellMap = this.aggs.get(node);
      if (!cellMap) {
        cellMap = new Map<string, CellEntry>();
        this.aggs.set(node, cellMap);
      }
      let entry = cellMap.get(colLeafKey);
      if (!entry) {
        entry = {
          states: this.measures.map((m) => createMeasureState(m.aggregation)),
          count: 0,
        };
        cellMap.set(colLeafKey, entry);
      }
      entry.count += 1;
      for (let i = 0; i < values.length; i += 1) {
        const v = values[i];
        if (v !== null) {
          entry.states[i].add(v);
        }
      }
    }
    this.records.set(id, { id, rowLabels, colLabels, values });
    return id;
  }

  addRecords(records: DataRecord[]): (string | number)[] {
    const ids: (string | number)[] = [];
    for (const record of records) {
      ids.push(this.addRecord(record));
    }
    return ids;
  }

  removeRecord(id: string | number): boolean {
    const stored = this.records.get(id);
    if (!stored) {
      return false;
    }
    this.records.delete(id);
    const rowNodes = this.rowTree.lookupPath(stored.rowLabels);
    const colLeafKey = JSON.stringify(stored.colLabels);
    for (const node of rowNodes) {
      const cellMap = this.aggs.get(node);
      if (!cellMap) {
        continue;
      }
      const entry = cellMap.get(colLeafKey);
      if (!entry) {
        continue;
      }
      entry.count -= 1;
      for (let i = 0; i < stored.values.length; i += 1) {
        const v = stored.values[i];
        if (v !== null) {
          entry.states[i].remove(v);
        }
      }
      if (entry.count <= 0) {
        cellMap.delete(colLeafKey);
      }
      if (cellMap.size === 0) {
        this.aggs.delete(node);
      }
    }
    this.rowTree.removePath(stored.rowLabels);
    this.colTree.removePath(stored.colLabels);
    return true;
  }

  clear(): void {
    this.records.clear();
    this.aggs.clear();
    this.rowTree.clear();
    this.colTree.clear();
    this.autoId = 0;
  }

  debugStats(): DebugStats {
    return {
      records: this.records.size,
      rowNodes: this.rowTree.size(),
      colNodes: this.colTree.size(),
      aggNodes: this.aggs.size,
    };
  }

  getResult(): PivotResult {
    const rowLayouts: RowLayout[] = [];
    this.buildRowLayouts(this.rowTree.root, [], rowLayouts);
    const grandDisplay: (string | null)[] = [];
    for (let i = 0; i < this.rowFields.length; i += 1) {
      if (i === 0) {
        grandDisplay.push(this.grandTotalLabel);
      } else {
        grandDisplay.push(null);
      }
    }
    rowLayouts.push({
      node: this.rowTree.root,
      displayPath: grandDisplay,
      path: [],
      isSubtotal: false,
      isGrandTotal: true,
      level: -1,
    });

    const colLayouts: ColumnLayout[] = [];
    const allColKeys = this.buildColumnLayouts(this.colTree.root, [], colLayouts);
    colLayouts.push({
      path: [this.grandTotalLabel],
      isSubtotal: false,
      isGrandTotal: true,
      level: this.colFields.length,
      leafKeys: allColKeys,
    });

    const columns: PivotColumn[] = colLayouts.map((c) => ({
      path: c.path,
      isSubtotal: c.isSubtotal,
      isGrandTotal: c.isGrandTotal,
      level: c.level,
    }));

    const rows: PivotRow[] = [];
    let prevDisplay: (string | null)[] = [];
    for (const layout of rowLayouts) {
      const headers: (string | null)[] = [];
      let changed = false;
      for (let i = 0; i < layout.displayPath.length; i += 1) {
        const current = layout.displayPath[i];
        if (current === null) {
          headers.push(null);
          continue;
        }
        const prev = i < prevDisplay.length ? prevDisplay[i] : null;
        if (!changed && prev === current) {
          headers.push(null);
        } else {
          changed = true;
          headers.push(current);
        }
      }
      prevDisplay = layout.displayPath;
      const cells: (number | null)[][] = [];
      for (const col of colLayouts) {
        cells.push(this.aggregateCells(layout.node, col.leafKeys));
      }
      rows.push({
        headers,
        path: layout.path,
        isSubtotal: layout.isSubtotal,
        isGrandTotal: layout.isGrandTotal,
        level: layout.level,
        cells,
      });
    }

    return {
      measures: this.measures.slice(),
      columns,
      rows,
      grandTotal: this.aggregateCells(this.rowTree.root, allColKeys),
    };
  }

  private buildRowLayouts(node: DimNode, prefix: string[], out: RowLayout[]): void {
    const children = this.sortedChildren(node, this.rowFields);
    for (const child of children) {
      const path = prefix.concat([child.label]);
      const isLeaf = child.level === this.rowFields.length - 1;
      if (isLeaf) {
        out.push({
          node: child,
          displayPath: path.slice(),
          path,
          isSubtotal: false,
          isGrandTotal: false,
          level: child.level,
        });
      } else {
        this.buildRowLayouts(child, path, out);
        const displayPath: (string | null)[] = [];
        for (let i = 0; i < this.rowFields.length; i += 1) {
          if (i < child.level) {
            displayPath.push(path[i]);
          } else if (i === child.level) {
            displayPath.push(path[i] + ' ' + this.subtotalLabel);
          } else {
            displayPath.push(null);
          }
        }
        out.push({
          node: child,
          displayPath,
          path,
          isSubtotal: true,
          isGrandTotal: false,
          level: child.level,
        });
      }
    }
  }

  private buildColumnLayouts(node: DimNode, prefix: string[], out: ColumnLayout[]): string[] {
    if (this.colFields.length === 0) {
      return ['[]'];
    }
    const allKeys: string[] = [];
    const children = this.sortedChildren(node, this.colFields);
    for (const child of children) {
      const path = prefix.concat([child.label]);
      const isLeaf = child.level === this.colFields.length - 1;
      if (isLeaf) {
        const key = JSON.stringify(path);
        out.push({
          path,
          isSubtotal: false,
          isGrandTotal: false,
          level: child.level,
          leafKeys: [key],
        });
        allKeys.push(key);
      } else {
        const childKeys = this.buildColumnLayouts(child, path, out);
        out.push({
          path,
          isSubtotal: true,
          isGrandTotal: false,
          level: child.level,
          leafKeys: childKeys.slice(),
        });
        for (const key of childKeys) {
          allKeys.push(key);
        }
      }
    }
    return allKeys;
  }

  private aggregateCells(node: DimNode, leafKeys: string[]): (number | null)[] {
    const cellMap = this.aggs.get(node);
    const merged: MeasureState[] = this.measures.map((m) => createMeasureState(m.aggregation));
    let found = false;
    if (cellMap) {
      for (const key of leafKeys) {
        const entry = cellMap.get(key);
        if (!entry) {
          continue;
        }
        found = true;
        for (let i = 0; i < merged.length; i += 1) {
          merged[i].merge(entry.states[i]);
        }
      }
    }
    if (!found) {
      return this.measures.map(() => null);
    }
    return merged.map((s) => s.value());
  }

  private sortedChildren(node: DimNode, fields: string[]): DimNode[] {
    const children = Array.from(node.children.values());
    const level = node.level + 1;
    const field: string | undefined = fields[level];
    if (field === undefined) {
      return children;
    }
    const spec = this.sorts[field];
    if (!spec) {
      return children;
    }
    let compare: SortComparator;
    if (typeof spec === 'function') {
      compare = spec;
    } else if (spec === 'desc') {
      compare = (a, b) => compareLabels(b, a);
    } else {
      compare = compareLabels;
    }
    return stableSort(children, (x, y) => compare(x.label, y.label));
  }

  private normalize(value: unknown): string {
    if (value === null || value === undefined) {
      return this.blankLabel;
    }
    if (typeof value === 'string') {
      return value === '' ? this.blankLabel : value;
    }
    return String(value);
  }

  private static toNumber(value: unknown): number | null {
    if (typeof value === 'number' && Number.isFinite(value)) {
      return value;
    }
    return null;
  }
}
