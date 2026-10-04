export const DEFAULT_NULL_LABEL = '(空)';

const SEP_KEY = '\u0001';
const SEP_PATH = '\u0002';
const SEP_CELL = '\u0003';

function nodeKey(level, label) {
  return level + SEP_KEY + label;
}

function makeNode(level, label, parentPathKey, order) {
  const key = nodeKey(level, label);
  return {
    level,
    label,
    key,
    pathKey: parentPathKey === null ? '' : parentPathKey + key + SEP_PATH,
    order,
    records: 0,
    children: new Map(),
  };
}

function prefixOf(path, length) {
  if (length === 0) return '';
  return path[length - 1].pathKey;
}

export class PivotTable {
  constructor(options) {
    if (!options.measures || options.measures.length === 0) {
      throw new Error('PivotTable requires at least one measure');
    }
    this.rowDims = options.rows ? [...options.rows] : [];
    this.colDims = options.columns ? [...options.columns] : [];
    this.measures = [...options.measures];
    this.nullLabel = options.nullLabel ?? DEFAULT_NULL_LABEL;
    this.rowSort = options.rowSort;
    this.colSort = options.columnSort;
    this.cells = new Map();
    this.records = new Map();
    this.nextRecordId = 1;
    this.nextOrder = 0;
    this.rowRoot = makeNode(-1, '', null, this.nextOrder++);
    this.colRoot = makeNode(-1, '', null, this.nextOrder++);
  }

  get size() {
    return this.records.size;
  }

  addRecord(record) {
    const rowLabels = this.rowDims.map((d) => this.toLabel(record[d]));
    const colLabels = this.colDims.map((d) => this.toLabel(record[d]));
    const values = this.measures.map((m) => this.measureValue(m, record));

    const rowPath = this.ensurePath(this.rowRoot, rowLabels);
    const colPath = this.ensurePath(this.colRoot, colLabels);

    for (let i = 0; i <= rowLabels.length; i++) {
      for (let j = 0; j <= colLabels.length; j++) {
        const key = prefixOf(rowPath, i) + SEP_CELL + prefixOf(colPath, j);
        let cell = this.cells.get(key);
        if (!cell) {
          cell = { records: 0, aggs: this.measures.map(() => ({ sum: 0, n: 0 })) };
          this.cells.set(key, cell);
        }
        cell.records++;
        for (let k = 0; k < values.length; k++) {
          const v = values[k];
          if (v !== null) {
            cell.aggs[k].sum += v;
            cell.aggs[k].n++;
          }
        }
      }
    }

    const id = this.nextRecordId++;
    this.records.set(id, { rowLabels, colLabels, values });
    return id;
  }

  retractRecord(id) {
    const stored = this.records.get(id);
    if (!stored) return false;
    this.records.delete(id);

    const rowPath = this.walkPath(this.rowRoot, stored.rowLabels);
    const colPath = this.walkPath(this.colRoot, stored.colLabels);

    for (let i = 0; i <= stored.rowLabels.length; i++) {
      for (let j = 0; j <= stored.colLabels.length; j++) {
        const key = prefixOf(rowPath, i) + SEP_CELL + prefixOf(colPath, j);
        const cell = this.cells.get(key);
        if (!cell) continue;
        cell.records--;
        for (let k = 0; k < stored.values.length; k++) {
          const v = stored.values[k];
          if (v !== null) {
            cell.aggs[k].sum -= v;
            cell.aggs[k].n--;
          }
        }
        if (cell.records === 0) {
          this.cells.delete(key);
        }
      }
    }

    this.prunePath(this.rowRoot, rowPath);
    this.prunePath(this.colRoot, colPath);
    return true;
  }

  render() {
    const colLeaves = [];
    if (this.colDims.length > 0) {
      this.collectLeaves(this.colRoot, [], colLeaves);
    }
    const columns = colLeaves.map((p) => ({
      kind: 'detail',
      path: p.map((n) => n.label),
      header: p.map((n) => n.label).join(' / '),
    }));
    columns.push({ kind: 'total', path: [], header: '总计' });

    const colKeys = colLeaves.map((p) => p[p.length - 1].pathKey);
    colKeys.push('');

    const rows = [];
    const lastLevel = this.rowDims.length - 1;
    const emit = (node, path) => {
      for (const child of this.sortChildren(node, this.rowSort, 'row')) {
        const childPath = [...path, child];
        if (child.level === lastLevel) {
          rows.push(this.makeRow('detail', child.level, childPath, colKeys));
        } else {
          emit(child, childPath);
          rows.push(this.makeRow('subtotal', child.level, childPath, colKeys));
        }
      }
    };
    emit(this.rowRoot, []);
    rows.push(this.makeRow('grandTotal', -1, [], colKeys));

    return {
      measures: this.measures.map((m) => m.label ?? `${m.agg}(${m.field})`),
      columns,
      rows,
    };
  }

  rowPaths() {
    const out = [];
    const walk = (node, path) => {
      for (const child of node.children.values()) {
        const next = [...path, child.label];
        out.push(next);
        walk(child, next);
      }
    };
    walk(this.rowRoot, []);
    return out;
  }

  toLabel(value) {
    if (value === null || value === undefined) return this.nullLabel;
    return String(value);
  }

  measureValue(measure, record) {
    if (measure.agg === 'count') return 1;
    const v = record[measure.field];
    if (typeof v === 'number' && !Number.isNaN(v)) return v;
    return null;
  }

  ensurePath(root, labels) {
    const path = [];
    let node = root;
    for (let i = 0; i < labels.length; i++) {
      const label = labels[i];
      const key = nodeKey(i, label);
      let child = node.children.get(key);
      if (!child) {
        child = makeNode(i, label, node.pathKey, this.nextOrder++);
        node.children.set(key, child);
      }
      child.records++;
      path.push(child);
      node = child;
    }
    return path;
  }

  walkPath(root, labels) {
    const path = [];
    let node = root;
    for (let i = 0; i < labels.length; i++) {
      const child = node.children.get(nodeKey(i, labels[i]));
      if (!child) break;
      path.push(child);
      node = child;
    }
    return path;
  }

  prunePath(root, path) {
    for (let i = path.length - 1; i >= 0; i--) {
      const node = path[i];
      node.records--;
      if (node.records === 0) {
        const parent = i === 0 ? root : path[i - 1];
        parent.children.delete(node.key);
      }
    }
  }

  collectLeaves(node, path, out) {
    if (node.level === this.colDims.length - 1) {
      out.push(path);
      return;
    }
    for (const child of this.sortChildren(node, this.colSort, 'column')) {
      this.collectLeaves(child, [...path, child], out);
    }
  }

  sortChildren(node, sort, axis) {
    const children = [...node.children.values()];
    if (!sort || !sort.direction || sort.direction === 'none') return children;
    const dir = sort.direction === 'desc' ? -1 : 1;
    const measureIndex = sort.measureIndex ?? 0;
    const decorated = children.map((child, index) => ({ child, index }));
    decorated.sort((a, b) => {
      const aNull = a.child.label === this.nullLabel;
      const bNull = b.child.label === this.nullLabel;
      if (aNull !== bNull) return aNull ? 1 : -1;
      let cmp = 0;
      if (sort.by === 'measure') {
        const av = this.nodeMeasureTotal(a.child, axis, measureIndex);
        const bv = this.nodeMeasureTotal(b.child, axis, measureIndex);
        if (av === null && bv === null) cmp = 0;
        else if (av === null) return 1;
        else if (bv === null) return -1;
        else cmp = av < bv ? -1 : av > bv ? 1 : 0;
      } else {
        cmp = a.child.label < b.child.label ? -1 : a.child.label > b.child.label ? 1 : 0;
      }
      if (cmp !== 0) return cmp * dir;
      return a.index - b.index;
    });
    return decorated.map((d) => d.child);
  }

  nodeMeasureTotal(node, axis, measureIndex) {
    const key = axis === 'row'
      ? node.pathKey + SEP_CELL
      : SEP_CELL + node.pathKey;
    const cell = this.cells.get(key);
    if (!cell) return null;
    return this.aggValue(cell.aggs[measureIndex], this.measures[measureIndex]);
  }

  makeRow(kind, depth, path, colKeys) {
    const rowKey = path.length === 0 ? '' : path[path.length - 1].pathKey;
    const labels = path.map((n) => n.label);
    let header;
    if (kind === 'grandTotal') header = '总计';
    else if (kind === 'subtotal') header = `${labels[labels.length - 1]} 小计`;
    else header = labels[labels.length - 1];
    const cells = colKeys.map((colKey) => {
      const cell = this.cells.get(rowKey + SEP_CELL + colKey);
      const values = this.measures.map((m, i) => (cell ? this.aggValue(cell.aggs[i], m) : null));
      return { values };
    });
    return { kind, depth, path: labels, header, cells };
  }

  aggValue(agg, measure) {
    if (agg.n === 0) return null;
    switch (measure.agg) {
      case 'count':
        return agg.n;
      case 'sum':
        return agg.sum;
      case 'avg':
        return agg.sum / agg.n;
    }
  }
}
