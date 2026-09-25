import {
  Lineage,
  LineageError,
  type JsonValue,
  type JsonObject,
  type NodeRef,
  type Relation,
  assertJsonCompatible,
  dedupeRefs,
  hasPointer,
  isJsonObject,
  joinPointer,
  appendPointer,
  escapePointer,
  pointerSegments,
  resolvePointer,
  walkJson,
} from "./types.ts";

export interface LoadTreeOptions {
  source: string;
  version: string;
  rowsPath?: string;
}

export interface RowContext {
  row: JsonValue;
  rowIndex: number;
  rowRef: NodeRef;
  at: (path?: string) => JsonValue;
  ref: (path?: string) => NodeRef;
}

export interface FilterOptions {
  version: string;
  predicate: (row: JsonValue, context: RowContext) => boolean;
  operation?: string;
}

export interface SplitOptions {
  version: string;
  field: string;
  as?: string;
  includeUnmatched?: boolean;
  operation?: string;
}

export interface AggregateOptions {
  version: string;
  groupBy?: string;
  keyAs?: string;
  aggregates: AggregateSpec[];
  operation?: string;
}

export type AggregateOperator = "count" | "sum" | "avg" | "min" | "max" | "collect";

export interface AggregateSpec {
  as: string;
  op: AggregateOperator;
  field?: string;
}

export interface JoinOptions {
  version: string;
  leftKey: string;
  rightKey: string;
  leftAs?: string;
  rightAs?: string;
  operation?: string;
}

export function loadTree(
  tree: JsonValue,
  options: LoadTreeOptions,
): Relation {
  const rowsPath = options.rowsPath ?? "/data";
  const rows = readRowsPath(tree, rowsPath, options.source, options.version);
  const lineage = new Lineage();
  lineage.addSourceVersion(
    { source: options.source, version: options.version, rowsPath },
    rows,
  );
  return { rows, version: options.version, lineage };
}

export function attachTree(
  lineage: Relation["lineage"],
  tree: JsonValue,
  options: LoadTreeOptions,
): Relation {
  const rowsPath = options.rowsPath ?? "/data";
  const rows = readRowsPath(tree, rowsPath, options.source, options.version);
  lineage.addSourceVersion(
    { source: options.source, version: options.version, rowsPath },
    rows,
  );
  return { rows, version: options.version, lineage };
}

export function filterRows(input: Relation, options: FilterOptions): Relation {
  const output: JsonValue[] = [];
  const builder = new MappingBuilder(input.version);
  builder.add("/", []);

  input.rows.forEach((row, rowIndex) => {
    const sourceRowPath = joinPointer("/", rowIndex);
    const context = createContext(row, rowIndex, input.version);
    let keep: boolean;
    try {
      keep = options.predicate(row, context);
    } catch (cause) {
      throw callbackError("Filter predicate failed", input.version, sourceRowPath, cause);
    }
    if (typeof keep !== "boolean") {
      throw operationError(
        "Filter predicate must return a boolean.",
        "INVALID_CALLBACK_RESULT",
        input.version,
        sourceRowPath,
      );
    }
    if (!keep) return;

    const outputIndex = output.length;
    output.push(structuredClone(row));
    copyTree(builder, joinPointer("/", outputIndex), sourceRowPath, row, input.version);
  });

  if (output.length > 0) builder.add("/", [{ version: input.version, path: "/" }]);
  return buildRelation(input, output, options.version, options.operation ?? "filter", builder);
}

export function splitRows(input: Relation, options: SplitOptions): Relation {
  const output: JsonValue[] = [];
  const builder = new MappingBuilder(input.version);
  builder.add("/", []);
  const targetField = options.as ?? options.field;

  input.rows.forEach((row, rowIndex) => {
    if (!isJsonObject(row)) {
      throw operationError(
        "Split requires each row to be an object.",
        "INVALID_ROW",
        input.version,
        joinPointer("/", rowIndex),
      );
    }
    const sourceRowPath = joinPointer("/", rowIndex);
    const fieldPath = appendPointer(sourceRowPath, fieldPointer(options.field));
    if (!hasPointer(row, fieldPointer(options.field))) {
      if (!options.includeUnmatched) {
        throw operationError(
          `Split field not found: ${options.field}`,
          "PATH_NOT_FOUND",
          input.version,
          fieldPath,
        );
      }
      const outputRow: JsonObject = { ...row };
      addSplitRow(
        builder,
        output,
        outputRow,
        sourceRowPath,
        input.version,
        targetField,
        undefined,
      );
      return;
    }

    const items = resolvePointer(row, fieldPointer(options.field));
    if (!Array.isArray(items)) {
      throw operationError(
        `Split field must be an array: ${options.field}`,
        "INVALID_SPLIT_FIELD",
        input.version,
        fieldPath,
      );
    }
    if (items.length === 0 && !options.includeUnmatched) return;

    items.forEach((item, itemIndex) => {
      const outputRow: JsonObject = { ...row };
      delete outputRow[options.field];
      outputRow[targetField] = structuredClone(item);
      addSplitRow(
        builder,
        output,
        outputRow,
        sourceRowPath,
        input.version,
        targetField,
        { sourceField: options.field, itemIndex, item },
      );
    });
  });

  if (output.length > 0) builder.add("/", [{ version: input.version, path: "/" }]);
  return buildRelation(input, output, options.version, options.operation ?? "split", builder);
}

export function aggregateRows(input: Relation, options: AggregateOptions): Relation {
  validateAggregateSpecs(options);
  const groups = buildGroups(input, options);
  const builder = new MappingBuilder(input.version);
  const output: JsonValue[] = [];
  builder.add("/", []);

  groups.forEach((group, groupIndex) => {
    const outRowPath = joinPointer("/", groupIndex);
    builder.add(outRowPath, group.refs);
    const outputRow: JsonObject = {};

    if (options.groupBy) {
      const keyAs = options.keyAs ?? options.groupBy;
      outputRow[keyAs] = group.key;
      const keyOutPath = joinPointer(outRowPath, keyAs);
      mapValueToRefs(builder, keyOutPath, group.key, group.keyRefs);
    }

    for (const spec of options.aggregates) {
      const value = calculateAggregate(input, group, spec, outRowPath);
      outputRow[spec.as] = value;
      const outPath = joinPointer(outRowPath, spec.as);
      if (spec.op === "count") {
        builder.add(outPath, group.refs);
      } else {
        const field = spec.field!;
        const refs = group.entries.map((entry) => ({
          version: input.version,
          path: appendPointer(entry.rowPath, fieldPointer(field)),
        }));
        if (spec.op === "collect" && Array.isArray(value)) {
          mapCollectedValues(builder, outPath, value, refs);
        } else {
          mapValueToRefs(builder, outPath, value, refs);
        }
      }
    }

    output.push(outputRow);
  });

  if (output.length > 0) builder.add("/", [{ version: input.version, path: "/" }]);
  return buildRelation(input, output, options.version, options.operation ?? "aggregate", builder);
}

export function joinRows(
  left: Relation,
  right: Relation,
  options: JoinOptions,
): Relation {
  if (left.lineage !== right.lineage) {
    throw new LineageError("Both sides of a join must share a Lineage graph.", {
      code: "LINEAGE_MISMATCH",
    });
  }
  const leftAs = options.leftAs ?? "left";
  const rightAs = options.rightAs ?? "right";
  if (leftAs === rightAs) {
    throw new LineageError("Join output aliases must be different.", {
      code: "INVALID_JOIN_ALIAS",
      path: `/${leftAs}`,
    });
  }

  const rightIndex = new Map<string, { row: JsonValue; rowPath: string }[]>();
  right.rows.forEach((row, index) => {
    const rowPath = joinPointer("/", index);
    const keyPath = appendPointer(rowPath, fieldPointer(options.rightKey));
    const key = readKey(row, options.rightKey, right.version, keyPath);
    const encoded = stableJson(key);
    const bucket = rightIndex.get(encoded) ?? [];
    bucket.push({ row, rowPath });
    rightIndex.set(encoded, bucket);
  });

  const output: JsonValue[] = [];
  const builder = new MappingBuilder(left.version);
  builder.add("/", []);

  left.rows.forEach((leftRow, leftIndex) => {
    const leftRowPath = joinPointer("/", leftIndex);
    const leftKeyPath = appendPointer(leftRowPath, fieldPointer(options.leftKey));
    const key = readKey(leftRow, options.leftKey, left.version, leftKeyPath);
    const matches = rightIndex.get(stableJson(key)) ?? [];

    for (const rightMatch of matches) {
      const outIndex = output.length;
      const outRowPath = joinPointer("/", outIndex);
      const outputRow: JsonObject = {
        [leftAs]: structuredClone(leftRow),
        [rightAs]: structuredClone(rightMatch.row),
      };
      output.push(outputRow);
      builder.add(outRowPath, [
        { version: left.version, path: leftRowPath },
        { version: right.version, path: rightMatch.rowPath },
      ]);
      copyTree(
        builder,
        joinPointer(outRowPath, leftAs),
        leftRowPath,
        leftRow,
        left.version,
      );
      copyTree(
        builder,
        joinPointer(outRowPath, rightAs),
        rightMatch.rowPath,
        rightMatch.row,
        right.version,
      );
    }
  });

  if (output.length > 0) {
    builder.add("/", [
      { version: left.version, path: "/" },
      { version: right.version, path: "/" },
    ]);
  }
  const lineage = left.lineage;
  lineage.addVersion(options.version, options.operation ?? "join", output, builder.mappings);
  return { rows: output, version: options.version, lineage };
}

class MappingBuilder {
  readonly mappings = new Map<string, NodeRef[]>();

  add(path: string, refs: Iterable<NodeRef>): void {
    const previous = this.mappings.get(path) ?? [];
    this.mappings.set(path, dedupeRefs([...previous, ...refs]));
  }
}

function createContext(row: JsonValue, rowIndex: number, version: string): RowContext {
  const rowRef = { version, path: joinPointer("/", rowIndex) };
  return {
    row,
    rowIndex,
    rowRef,
    at(path = "/") {
      return resolvePointer(row, path);
    },
    ref(path = "/") {
      if (path === "/") return { ...rowRef };
      return { version, path: [rowRef.path, ...pointerSegments(path).map(escapePointer)].join("/") };
    },
  };
}

function copyTree(
  builder: MappingBuilder,
  outBase: string,
  inBase: string,
  value: JsonValue,
  version: string,
): void {
  walkJson(value, outBase, (outPath, _item) => {
    const suffix = outPath === outBase ? "/" : outPath.slice(outBase.length);
    const inPath = suffix === "/" ? inBase : `${inBase}${suffix}`;
    builder.add(outPath, [{ version, path: inPath }]);
  });
}

function mapValueToRefs(
  builder: MappingBuilder,
  outBase: string,
  value: JsonValue,
  refs: NodeRef[],
): void {
  const uniqueRefs = dedupeRefs(refs);
  walkJson(value, outBase, (outPath, _item) => {
    const suffix = outPath === outBase ? "" : outPath.slice(outBase.length);
    builder.add(
      outPath,
      uniqueRefs.map((ref) =>
        suffix === "" ? ref : { version: ref.version, path: `${ref.path}${suffix}` },
      ),
    );
  });
}

function mapCollectedValues(
  builder: MappingBuilder,
  outBase: string,
  values: JsonValue[],
  valueRefs: NodeRef[],
): void {
  builder.add(outBase, valueRefs);
  values.forEach((value, index) => {
    mapValueToRefs(
      builder,
      joinPointer(outBase, index),
      value,
      [valueRefs[index]!],
    );
  });
}

interface GroupEntry {
  row: JsonValue;
  rowPath: string;
  keyRef: NodeRef;
}

interface Group {
  key: JsonValue;
  keyRefs: NodeRef[];
  refs: NodeRef[];
  entries: GroupEntry[];
}

function buildGroups(input: Relation, options: AggregateOptions): Group[] {
  const groups: Group[] = [];
  const index = new Map<string, Group>();

  input.rows.forEach((row, rowIndex) => {
    const rowPath = joinPointer("/", rowIndex);
    let key: JsonValue = null;
    let keyRef: NodeRef = { version: input.version, path: rowPath };

    if (options.groupBy) {
      const keyPath = appendPointer(rowPath, fieldPointer(options.groupBy));
      key = readKey(row, options.groupBy, input.version, keyPath);
      keyRef = { version: input.version, path: keyPath };
    }

    const canonical = stableJson(key);
    let group = index.get(canonical);
    if (!group) {
      group = {
        key,
        keyRefs: [],
        refs: [],
        entries: [],
      };
      index.set(canonical, group);
      groups.push(group);
    }
    group.entries.push({ row, rowPath, keyRef });
    group.refs.push({ version: input.version, path: rowPath });
    group.keyRefs.push(keyRef);
  });

  return groups;
}

function validateAggregateSpecs(options: AggregateOptions): void {
  const names = new Set<string>();
  for (const spec of options.aggregates) {
    if (!spec.as || names.has(spec.as)) {
      throw new LineageError(`Duplicate or empty aggregate field: ${spec.as}`, {
        code: "INVALID_AGGREGATE_SPEC",
        version: options.version,
        path: spec.as ? `/${spec.as}` : "/",
      });
    }
    names.add(spec.as);
    if (spec.op !== "count" && !spec.field) {
      throw new LineageError(`Aggregate ${spec.op} requires a field.`, {
        code: "INVALID_AGGREGATE_SPEC",
        version: options.version,
        path: `/${spec.as}`,
      });
    }
    if (options.groupBy && spec.as === (options.keyAs ?? options.groupBy)) {
      throw new LineageError("Aggregate output field collides with group key.", {
        code: "INVALID_AGGREGATE_SPEC",
        version: options.version,
        path: `/${spec.as}`,
      });
    }
  }
}

function calculateAggregate(
  input: Relation,
  group: Group,
  spec: AggregateSpec,
  outRowPath: string,
): JsonValue {
  const outPath = joinPointer(outRowPath, spec.as);
  if (spec.op === "count") return group.entries.length;

  const field = spec.field!;
  const values = group.entries.map((entry) => {
    const fieldPath = appendPointer(entry.rowPath, fieldPointer(field));
    const relative = fieldPointer(field);
    if (!hasPointer(entry.row, relative)) {
      throw operationError(
        `Aggregate field not found: ${field}`,
        "PATH_NOT_FOUND",
        input.version,
        fieldPath,
      );
    }
    const value = resolvePointer(entry.row, relative);
    return value;
  });

  switch (spec.op) {
    case "collect":
      return values;
    case "sum": {
      const numeric = values.map((value, index) =>
        requireNumber(value, input.version, joinPointer(outPath, index), spec.op),
      );
      return numeric.reduce((total, value) => total + value, 0);
    }
    case "avg": {
      const numeric = values.map((value, index) =>
        requireNumber(value, input.version, joinPointer(outPath, index), spec.op),
      );
      return numeric.reduce((total, value) => total + value, 0) / numeric.length;
    }
    case "min":
      return values.reduce((best, value) => {
        requireComparable(value, input.version, outPath, spec.op);
        if (typeof best === "boolean" || typeof value === "boolean") {
          throw operationError("min does not support boolean values.", "INVALID_VALUE", input.version, outPath);
        }
        return value < best ? value : best;
      });
    case "max":
      return values.reduce((best, value) => {
        requireComparable(value, input.version, outPath, spec.op);
        if (typeof best === "boolean" || typeof value === "boolean") {
          throw operationError("max does not support boolean values.", "INVALID_VALUE", input.version, outPath);
        }
        return value > best ? value : best;
      });
    default:
      return exhaust(spec.op);
  }
}

function requireNumber(value: JsonValue, version: string, path: string, op: string): number {
  if (typeof value !== "number") {
    throw operationError(`${op} requires numeric values.`, "INVALID_VALUE", version, path);
  }
  return value;
}

function requireComparable(value: JsonValue, version: string, path: string, op: string): void {
  if (typeof value !== "number" && typeof value !== "string") {
    throw operationError(`${op} requires number or string values.`, "INVALID_VALUE", version, path);
  }
}

function exhaust(value: never): never {
  throw new LineageError(`Unsupported aggregate operator: ${String(value)}`, {
    code: "INVALID_AGGREGATE_SPEC",
  });
}

function readKey(row: JsonValue, field: string, version: string, path: string): JsonValue {
  const relative = fieldPointer(field);
  if (!isJsonObject(row) || !hasPointer(row, relative)) {
    throw operationError(`Key field not found: ${field}`, "PATH_NOT_FOUND", version, path);
  }
  return resolvePointer(row, relative);
}

function stableJson(value: JsonValue): string {
  if (value === null) return "null";
  if (typeof value === "number") return `n:${String(value)}`;
  if (typeof value === "boolean") return `b:${String(value)}`;
  if (typeof value === "string") return `s:${value}`;
  if (Array.isArray(value)) return `a[${value.map(stableJson).join(",")}]`;
  const keys = Object.keys(value).sort();
  return `o{${keys.map((key) => `${stableJson(key)}:${stableJson(value[key]!)}`).join(",")}}`;
}

function addSplitRow(
  builder: MappingBuilder,
  output: JsonValue[],
  outputRow: JsonObject,
  sourceRowPath: string,
  version: string,
  targetField: string,
  item: { sourceField: string; itemIndex: number; item: JsonValue } | undefined,
): void {
  const outputIndex = output.length;
  const outRowPath = joinPointer("/", outputIndex);
  output.push(outputRow);
  builder.add(outRowPath, [{ version, path: sourceRowPath }]);

  for (const [key, child] of Object.entries(outputRow)) {
    const outFieldPath = joinPointer(outRowPath, key);
    if (item && (key === targetField || key === item.sourceField)) {
      const sourceItemPath = appendPointer(
        sourceRowPath,
        appendPointer(fieldPointer(item.sourceField), joinPointer("/", item.itemIndex)),
      );
      mapValueToRefs(builder, outFieldPath, child, [{ version, path: sourceItemPath }]);
    } else {
      const sourceFieldPath = joinPointer(sourceRowPath, key);
      mapValueToRefs(builder, outFieldPath, child, [{ version, path: sourceFieldPath }]);
    }
  }
}

function buildRelation(
  input: Relation,
  rows: JsonValue[],
  version: string,
  operation: string,
  builder: MappingBuilder,
): Relation {
  input.lineage.addVersion(version, operation, rows, builder.mappings);
  return { rows, version, lineage: input.lineage };
}

function fieldPointer(field: string): string {
  const normalized = field.startsWith("/") ? field : `/${field}`;
  return pointerSegments(normalized).reduce<string>(
    (pointer, segment) => joinPointer(pointer, segment),
    "/",
  );
}

function operationError(
  message: string,
  code: string,
  version: string,
  path: string,
  cause?: unknown,
  source?: string,
): LineageError {
  return new LineageError(message, {
    code,
    version,
    path,
    cause,
    locations: [{ source, version, path }],
  });
}

function callbackError(message: string, version: string, path: string, cause: unknown): LineageError {
  const locations = cause instanceof LineageError ? cause.locations : [{ version, path }];
  return new LineageError(message, {
    code: "CALLBACK_ERROR",
    version,
    path,
    cause,
    locations,
  });
}

function assertTreeJsonCompatible(
  tree: unknown,
  source: string,
  version: string,
): void {
  assertJsonCompatible(tree, { source, version, path: "/" });
}

function readRowsPath(
  tree: JsonValue,
  rowsPath: string,
  source: string,
  version: string,
): JsonValue[] {
  assertTreeJsonCompatible(tree, source, version);
  let rows: JsonValue;
  try {
    rows = resolvePointer(tree, rowsPath);
  } catch (cause) {
    if (cause instanceof LineageError) {
      throw new LineageError(`Rows path cannot be read: ${rowsPath}`, {
        code: cause.code,
        version,
        path: rowsPath,
        cause,
        locations: [{ source, version, path: rowsPath }],
      });
    }
    throw cause;
  }
  if (!Array.isArray(rows)) {
    throw operationError(
      `Rows path ${rowsPath} must point to an array.`,
      "INVALID_ROWS",
      version,
      rowsPath,
      undefined,
      source,
    );
  }

  rows.forEach((_row, index) => {
    assertJsonCompatible(_row, {
      source,
      version,
      path: joinPointer(rowsPath, index),
    });
  });
  return rows;
}
