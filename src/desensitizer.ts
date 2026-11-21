import { randomBytes as nodeRandomBytes } from "node:crypto";
import {
  AuditVault,
  deepClone,
  hashValue,
} from "./vault.ts";
import {
  CircularReferenceError,
  DesensitizationError,
  DesensitizationLeakError,
} from "./errors.ts";
import { FieldResolver } from "./fields.ts";
import {
  DEFAULT_LEVEL_STRATEGIES,
  isPublic,
  LEVEL_RANK,
  maxLevel,
  normalizeLevel,
} from "./levels.ts";
import type {
  AuditEntry,
  DesensitizerOptions,
  MaskingStrategy,
  NestedMode,
  SensitivityLevel,
  SerializeOptions,
} from "./types.ts";
import {
  buildVariantIndex,
  findLeaksWithIndex,
  findSensitiveSpans,
  type VariantIndex,
} from "./variants.ts";

const OMIT = Symbol("desensitizer.omit");
const TOKEN_DELIMITERS: ReadonlyArray<readonly [string, string]> = [
  ["{{", "}}"],
  ["[[", "]]"],
  ["<<", ">>"],
  ["«", "»"],
  ["⟦", "⟧"],
  ["\u0001", "\u0002"],
];

type NormalizedValue = unknown | typeof OMIT;

function toPointer(segments: ReadonlyArray<string | number>): string {
  if (segments.length === 0) return "";
  return `/${segments
    .map((segment) =>
      String(segment).replace(/~/g, "~0").replace(/\//g, "~1"),
    )
    .join("/")}`;
}

function primitiveText(value: unknown): string | undefined {
  if (typeof value === "string") return value;
  if (
    typeof value === "number" ||
    typeof value === "bigint" ||
    typeof value === "boolean"
  ) {
    return String(value);
  }
  return undefined;
}

function setOwn(
  target: Record<string, unknown>,
  key: string,
  value: unknown,
): void {
  Object.defineProperty(target, key, {
    value,
    enumerable: true,
    writable: true,
    configurable: true,
  });
}

interface ResolvedOptions {
  resolver: FieldResolver;
  defaultLevel: SensitivityLevel;
  levelStrategies: Record<SensitivityLevel, MaskingStrategy>;
  nestedMode: NestedMode;
  detection: DesensitizerOptions["detection"];
  knownSensitiveValues: unknown[];
  verifyOutput: boolean;
  storeOriginal: boolean;
  randomBytes: (size: number) => Uint8Array;
}

interface SplitLeaf {
  value: string;
  encoding: string;
}

interface BuildContext {
  index: VariantIndex;
  splitLeaves: Map<string, SplitLeaf>;
  valueLevels: Map<string, SensitivityLevel>;
  options: ResolvedOptions;
}

interface LeafUnit {
  path: string;
  text: string;
  original: unknown;
}

interface TokenMeta {
  original: unknown;
  level: SensitivityLevel;
  path: string;
  reason: "field" | "subtree" | "content" | "split" | "key" | "known-value";
  strategy?: MaskingStrategy;
  sensitiveValue?: string;
  encoding?: string;
  field?: string;
}

function normalizeValue(
  value: unknown,
  key: string | undefined,
  stack: Set<object>,
  path: ReadonlyArray<string | number>,
  skipToJSON = false,
): NormalizedValue {
  if (value === null) return null;

  const type = typeof value;
  if (type === "string" || type === "number" || type === "boolean") {
    return value;
  }
  if (type === "bigint") {
    return String(value);
  }
  if (type === "undefined" || type === "function" || type === "symbol") {
    return OMIT;
  }

  const object = value as Record<string, unknown>;
  if (stack.has(object)) {
    throw new CircularReferenceError(toPointer(path) || "/");
  }

  if (!skipToJSON && typeof object.toJSON === "function") {
    stack.add(object);
    try {
      const result = object.toJSON(key);
      if (result !== object) {
        return normalizeValue(result, key, stack, path);
      }
      return normalizeValue(object, key, stack, path, true);
    } finally {
      stack.delete(object);
    }
  }

  if (value instanceof Date) {
    return Number.isNaN(value.getTime()) ? null : value.toISOString();
  }

  if (value instanceof Map) {
    stack.add(object);
    try {
      const output: Record<string, unknown> = {};
      for (const [mapKey, mapValue] of value.entries()) {
        const normalized = normalizeValue(
          mapValue,
          String(mapKey),
          stack,
          [...path, String(mapKey)],
        );
        if (normalized !== OMIT) {
          setOwn(output, String(mapKey), normalized);
        }
      }
      return output;
    } finally {
      stack.delete(object);
    }
  }

  if (value instanceof Set) {
    stack.add(object);
    try {
      return [...value].map((item, index) => {
        const normalized = normalizeValue(item, String(index), stack, [
          ...path,
          index,
        ]);
        return normalized === OMIT ? null : normalized;
      });
    } finally {
      stack.delete(object);
    }
  }

  if (ArrayBuffer.isView(value) || value instanceof ArrayBuffer) {
    stack.add(object);
    try {
      const items =
        value instanceof ArrayBuffer
          ? [...new Uint8Array(value)]
          : (() => {
              const view = value as ArrayBufferView;
              return [
                ...new Uint8Array(
                  view.buffer,
                  view.byteOffset,
                  view.byteLength,
                ),
              ];
            })();
      return items.map((item, index) => {
        const normalized = normalizeValue(item, String(index), stack, [
          ...path,
          index,
        ]);
        return normalized === OMIT ? null : normalized;
      });
    } finally {
      stack.delete(object);
    }
  }

  if (Array.isArray(value)) {
    stack.add(object);
    try {
      return value.map((item, index) => {
        const normalized = normalizeValue(item, String(index), stack, [
          ...path,
          index,
        ]);
        return normalized === OMIT ? null : normalized;
      });
    } finally {
      stack.delete(object);
    }
  }

  stack.add(object);
  try {
    const output: Record<string, unknown> = {};
    for (const childKey of Object.keys(object)) {
      const normalized = normalizeValue(
        object[childKey],
        childKey,
        stack,
        [...path, childKey],
      );
      if (normalized !== OMIT) {
        setOwn(output, childKey, normalized);
      }
    }
    return output;
  } finally {
    stack.delete(object);
  }
}

function registerValue(
  output: Map<string, SensitivityLevel>,
  value: unknown,
  level: SensitivityLevel,
): void {
  const text = primitiveText(value);
  if (text === undefined || text.length === 0) return;
  const previous = output.get(text);
  output.set(text, previous ? maxLevel(previous, level) : level);
}

function collectPrimitiveValues(
  value: unknown,
  level: SensitivityLevel,
  output: Map<string, SensitivityLevel>,
): void {
  if (value === null || value === undefined) return;
  const text = primitiveText(value);
  if (text !== undefined) {
    registerValue(output, value, level);
    return;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      collectPrimitiveValues(item, level, output);
    }
    return;
  }
  if (typeof value === "object") {
    for (const item of Object.values(value as Record<string, unknown>)) {
      collectPrimitiveValues(item, level, output);
    }
  }
}

function collectSensitiveValues(
  node: unknown,
  inheritedLevel: SensitivityLevel,
  pointer: ReadonlyArray<string | number>,
  pathNames: readonly string[],
  resolver: FieldResolver,
  output: Map<string, SensitivityLevel>,
): void {
  if (node === null || node === undefined) return;

  if (Array.isArray(node)) {
    node.forEach((item, index) =>
      collectSensitiveValues(
        item,
        inheritedLevel,
        [...pointer, index],
        pathNames,
        resolver,
        output,
      ),
    );
    return;
  }

  if (typeof node === "object") {
    for (const [key, child] of Object.entries(
      node as Record<string, unknown>,
    )) {
      const resolved = resolver.resolve(
        key,
        [...pathNames, key],
        typeof child,
      );
      const level = maxLevel(inheritedLevel, resolved.level);
      if (!isPublic(level)) {
        collectPrimitiveValues(child, level, output);
      }
      collectSensitiveValues(
        child,
        level,
        [...pointer, key],
        [...pathNames, key],
        resolver,
        output,
      );
    }
    return;
  }

  if (!isPublic(inheritedLevel)) {
    registerValue(output, node, inheritedLevel);
  }
}

function collectLeafUnits(
  node: unknown,
  inheritedLevel: SensitivityLevel,
  pointer: ReadonlyArray<string | number>,
  pathNames: readonly string[],
  resolver: FieldResolver,
  units: LeafUnit[],
): void {
  if (node === null || node === undefined) return;

  if (Array.isArray(node)) {
    if (!isPublic(inheritedLevel)) return;
    node.forEach((item, index) =>
      collectLeafUnits(
        item,
        inheritedLevel,
        [...pointer, index],
        pathNames,
        resolver,
        units,
      ),
    );
    return;
  }

  if (typeof node === "object") {
    if (!isPublic(inheritedLevel)) return;
    for (const [key, child] of Object.entries(
      node as Record<string, unknown>,
    )) {
      const resolved = resolver.resolve(
        key,
        [...pathNames, key],
        typeof child,
      );
      const level = maxLevel(inheritedLevel, resolved.level);
      collectLeafUnits(
        child,
        level,
        [...pointer, key],
        [...pathNames, key],
        resolver,
        units,
      );
    }
    return;
  }

  if (!isPublic(inheritedLevel)) return;
  const text = primitiveText(node);
  if (text === undefined || text.length === 0) return;
  units.push({ path: toPointer(pointer), text, original: node });
}

function buildSplitLeaves(
  normalized: unknown,
  resolver: FieldResolver,
  index: VariantIndex,
  initialLevel: SensitivityLevel,
): Map<string, SplitLeaf> {
  const splitLeaves = new Map<string, SplitLeaf>();
  if (!index.options.splitAcrossFields) return splitLeaves;

  const records: Array<{
    value: unknown;
    pointer: ReadonlyArray<string | number>;
  }> = [];
  if (Array.isArray(normalized)) {
    records.push({ value: normalized, pointer: [] });
    normalized.forEach((item, position) =>
      records.push({ value: item, pointer: [position] }),
    );
  } else {
    records.push({ value: normalized, pointer: [] });
  }

  for (const record of records) {
    const units: LeafUnit[] = [];
    collectLeafUnits(
      record.value,
      initialLevel,
      record.pointer,
      [],
      resolver,
      units,
    );
    if (units.length < 2) continue;

    const combined = units.map((unit) => unit.text).join("");
    if (combined.length === 0) continue;
    const offsets: number[] = [];
    let cursor = 0;
    for (const unit of units) {
      offsets.push(cursor);
      cursor += unit.text.length;
    }

    for (const span of findSensitiveSpans(combined, index, {
      wholeTextTransforms: false,
    })) {
      let first = -1;
      let last = -1;
      for (let position = 0; position < units.length; position += 1) {
        const start = offsets[position];
        const end = start + units[position].text.length;
        if (end <= span.start) continue;
        if (start >= span.end) break;
        if (first === -1) first = position;
        last = position;
      }
      if (first === -1 || last <= first) continue;
      for (let position = first; position <= last; position += 1) {
        const unit = units[position];
        if (!splitLeaves.has(unit.path)) {
          splitLeaves.set(unit.path, {
            value: span.value,
            encoding: `split>${span.encoding}`,
          });
        }
      }
    }
  }

  return splitLeaves;
}

function makePreview(original: unknown): string {
  let text =
    typeof original === "string"
      ? original
      : JSON.stringify(original) ?? String(original);
  text = text.replace(/[{}]/g, "*").replace(/\s+/g, " ");
  text = text.replace(/[{}[\]<>«»⟦⟧\u0001\u0002]/g, "*");
  const length = Array.from(text).length;
  if (length <= 4) return "****";
  if (length <= 8) {
    const characters = Array.from(text);
    return `${characters[0]}****${characters[characters.length - 1]}`;
  }
  const characters = Array.from(text);
  const head = characters.slice(0, 3).join("");
  const tail = characters.slice(-4).join("");
  return `${head}${"*".repeat(Math.max(2, length - 7))}${tail}`.slice(0, 32);
}

export class Desensitizer {
  readonly vault: AuditVault;
  private readonly options: ResolvedOptions;
  private lastResult: SerializationResult | undefined;

  constructor(options: DesensitizerOptions = {}) {
    const levelStrategies: Record<SensitivityLevel, MaskingStrategy> = {
      ...DEFAULT_LEVEL_STRATEGIES,
      ...(options.levelStrategies ?? {}),
    };
    this.options = {
      resolver: new FieldResolver({
        fields:
          options.fields ??
          options.sensitiveFields ??
          options.fieldLevels,
        defaultLevel: normalizeLevel(options.defaultLevel ?? "public"),
        inferSensitiveFields: options.inferSensitiveFields ?? true,
      }),
      defaultLevel: normalizeLevel(options.defaultLevel ?? "public"),
      levelStrategies,
      nestedMode: options.nestedMode ?? "leaves",
      detection: options.detection,
      knownSensitiveValues: [...(options.knownSensitiveValues ?? [])],
      verifyOutput: options.verifyOutput ?? true,
      storeOriginal: options.storeOriginal ?? true,
      randomBytes: options.randomBytes ?? ((size) => nodeRandomBytes(size)),
    };
    this.vault = new AuditVault();
  }

  resetAudit(): void {
    this.vault.clear();
    this.lastResult = undefined;
  }

  getAudit(): readonly AuditEntry[] {
    return this.lastResult?.audit ?? [];
  }

  reveal(token: string): unknown {
    return this.vault.reveal(token);
  }

  desensitize(value: unknown): unknown {
    const start = this.vault.size;
    const { masked, index } = this.process(value);
    if (this.options.verifyOutput) {
      const leaks = findLeaksWithIndex(masked, index);
      if (leaks.length > 0) {
        throw new DesensitizationLeakError(leaks);
      }
    }
    this.lastResult = new SerializationResult(
      "",
      this.snapshotAudit(start),
      this.vault,
    );
    return masked;
  }

  serialize(value: unknown, options: SerializeOptions = {}): string {
    return this.serializeWithAudit(value, options).body;
  }

  serializeWithAudit(
    value: unknown,
    options: SerializeOptions = {},
  ): SerializationResult {
    const start = this.vault.size;
    const { masked, index } = this.process(value);
    const body =
      JSON.stringify(masked, null, options.space) ?? "null";

    if (this.options.verifyOutput) {
      const leaks = findLeaksWithIndex(body, index);
      if (leaks.length > 0) {
        throw new DesensitizationLeakError(leaks);
      }
    }

    const result = new SerializationResult(
      body,
      this.snapshotAudit(start),
      this.vault,
    );
    this.lastResult = result;
    return result;
  }

  restore<T>(value: T): T {
    return this.restoreNode(value) as T;
  }

  deserialize<T = unknown>(body: string): T {
    return this.restore(JSON.parse(body) as T);
  }

  private process(value: unknown): {
    masked: unknown;
    index: VariantIndex;
  } {
    const normalized = normalizeValue(
      value,
      undefined,
      new Set<object>(),
      [],
    );
    const safeNormalized = normalized === OMIT ? null : normalized;

    const valueLevels = new Map<string, SensitivityLevel>();
    collectSensitiveValues(
      safeNormalized,
      this.options.defaultLevel,
      [],
      [],
      this.options.resolver,
      valueLevels,
    );
    for (const known of this.options.knownSensitiveValues) {
      collectPrimitiveValues(known, "sensitive", valueLevels);
    }

    const index = buildVariantIndex(
      valueLevels.keys(),
      this.options.detection,
    );
    const splitLeaves = buildSplitLeaves(
      safeNormalized,
      this.options.resolver,
      index,
      this.options.defaultLevel,
    );
    const context: BuildContext = {
      index,
      splitLeaves,
      valueLevels,
      options: this.options,
    };

    const masked = this.buildNode(
      safeNormalized,
      [],
      [],
      this.options.defaultLevel,
      undefined,
      undefined,
      undefined,
      context,
    );
    return { masked, index };
  }

  private buildNode(
    node: unknown,
    pointer: ReadonlyArray<string | number>,
    pathNames: readonly string[],
    inheritedLevel: SensitivityLevel,
    sensitiveField: string | undefined,
    sensitiveMode: NestedMode | undefined,
    sensitiveStrategy: MaskingStrategy | undefined,
    context: BuildContext,
  ): unknown {
    if (node === null || node === undefined) return node;

    if (Array.isArray(node)) {
      return node.map((item, index) =>
        this.buildNode(
          item,
          [...pointer, index],
          pathNames,
          inheritedLevel,
          sensitiveField,
          sensitiveMode,
          sensitiveStrategy,
          context,
        ),
      );
    }

    if (typeof node === "object") {
      const output: Record<string, unknown> = {};
      for (const [key, child] of Object.entries(
        node as Record<string, unknown>,
      )) {
        const resolved = context.options.resolver.resolve(
          key,
          [...pathNames, key],
          typeof child,
        );
        const level = maxLevel(inheritedLevel, resolved.level);
        const mode =
          resolved.nestedMode ??
          sensitiveMode ??
          context.options.nestedMode;
        const effectiveMode = isPublic(level) ? undefined : mode;
        const effectiveStrategy = isPublic(level)
          ? undefined
          : resolved.strategy ?? sensitiveStrategy;
        const field =
          !isPublic(resolved.level) &&
          LEVEL_RANK[resolved.level] > LEVEL_RANK[inheritedLevel]
            ? key
            : sensitiveField;
        const childPointer = [...pointer, key];
        const pointerText = toPointer(childPointer);
        const split = context.splitLeaves.get(pointerText);
        let masked: unknown;

        if (
          !isPublic(level) &&
          mode === "subtree" &&
          child !== null &&
          typeof child === "object"
        ) {
          masked = this.tokenFor({
            original: child,
            level,
            path: pointerText,
            reason: "subtree",
            strategy: resolved.strategy ?? sensitiveStrategy,
            sensitiveValue: primitiveText(child),
            field,
          }, context);
        } else if (!isPublic(level) && primitiveText(child) !== undefined) {
          masked = this.tokenFor({
            original: child,
            level,
            path: pointerText,
            reason: "field",
            strategy: resolved.strategy ?? sensitiveStrategy,
            sensitiveValue: primitiveText(child),
            field,
          }, context);
        } else if (isPublic(level) && split) {
          masked = this.tokenFor({
            original: child,
            level:
              context.valueLevels.get(split.value) ?? "sensitive",
            path: pointerText,
            reason: "split",
            sensitiveValue: split.value,
            encoding: split.encoding,
            field,
          }, context);
        } else {
          masked = this.buildNode(
            child,
            childPointer,
            [...pathNames, key],
            level,
            field,
            effectiveMode,
            effectiveStrategy,
            context,
          );
        }

        const safeKey = this.sanitizeText(
          key,
          [...pointer, key],
          context,
          "key",
        );
        setOwn(output, safeKey, masked);
      }
      return output;
    }

    if (!isPublic(inheritedLevel)) {
      return this.tokenFor({
        original: node,
        level: inheritedLevel,
        path: toPointer(pointer),
        reason: "field",
        strategy: sensitiveStrategy,
        sensitiveValue: primitiveText(node),
        field: sensitiveField,
      }, context);
    }

    const pointerText = toPointer(pointer);
    const split = context.splitLeaves.get(pointerText);
    if (split) {
      return this.tokenFor({
        original: node,
        level: context.valueLevels.get(split.value) ?? "sensitive",
        path: pointerText,
        reason: "split",
        sensitiveValue: split.value,
        encoding: split.encoding,
      }, context);
    }

    if (typeof node === "string") {
      return this.sanitizeText(node, pointer, context);
    }

    const text = primitiveText(node);
    if (text !== undefined) {
      const spans = findSensitiveSpans(text, context.index);
      if (spans.length > 0) {
        const value = spans[0].value;
        return this.tokenFor({
          original: node,
          level: context.valueLevels.get(value) ?? "sensitive",
          path: pointerText,
          reason: "content",
          sensitiveValue: value,
          encoding: spans[0].encoding,
        }, context);
      }
    }

    return node;
  }

  private sanitizeText(
    text: string,
    pointer: ReadonlyArray<string | number>,
    context: BuildContext,
    reason: "content" | "key" = "content",
  ): string {
    const spans = findSensitiveSpans(text, context.index);
    if (spans.length === 0) return text;

    let output = "";
    let cursor = 0;
    for (const span of spans) {
      output += text.slice(cursor, span.start);
      const original = text.slice(span.start, span.end);
      const level =
        context.valueLevels.get(span.value) ?? "sensitive";
      output += this.tokenFor({
        original,
        level,
        path: toPointer(pointer),
        reason,
        sensitiveValue: span.value,
        encoding: span.encoding,
      }, context);
      cursor = span.end;
    }
    output += text.slice(cursor);
    return output;
  }

  private tokenFor(meta: TokenMeta, context: BuildContext): string {
    let strategy =
      meta.strategy ?? context.options.levelStrategies[meta.level];

    let preview = strategy === "partial" ? makePreview(meta.original) : "";
    let token = "";

    for (
      let attempt = 0;
      attempt < TOKEN_DELIMITERS.length * 4;
      attempt += 1
    ) {
      const [open, close] =
        TOKEN_DELIMITERS[attempt % TOKEN_DELIMITERS.length];
      const random = Buffer.from(context.options.randomBytes(12));
      const id = random.toString("base64url");
      if (!id) {
        throw new DesensitizationError(
          "randomBytes must return at least one byte",
        );
      }
      if (strategy === "hash") {
        const digest = hashValue(meta.original).slice(0, 16);
        token = `${open}h${digest}_${id.slice(0, 8)}${close}`;
      } else if (strategy === "partial") {
        token = `${open}${id}:${preview}${close}`;
      } else {
        token = `${open}${id}${close}`;
      }

      const tokenLeaks = findSensitiveSpans(token, context.index);
      const rawCollision = context.index.values.some(
        (value) => value.length > 0 && token.includes(value),
      );
      if (
        tokenLeaks.length === 0 &&
        !rawCollision &&
        !this.vault.has(token)
      ) {
        break;
      }

      if (strategy === "partial" && preview !== "****") {
        preview = "****";
      }
      if (
        attempt >= TOKEN_DELIMITERS.length &&
        strategy !== "tokenize"
      ) {
        strategy = "tokenize";
        preview = "";
      }
      token = "";
    }

    if (!token) {
      throw new DesensitizationError(
        "Unable to generate a token that avoids all sensitive variants",
      );
    }

    this.vault.record({
      token,
      path: meta.path,
      level: meta.level,
      reason: meta.reason,
      original: context.options.storeOriginal ? meta.original : undefined,
      sensitiveValue: meta.sensitiveValue,
      encoding: meta.encoding,
      strategy,
      field: meta.field,
      createdAt: Date.now(),
    });
    return token;
  }

  private restoreNode(value: unknown): unknown {
    if (typeof value === "string") {
      if (this.vault.has(value)) {
        return this.vault.reveal(value);
      }
      return this.vault.replaceTokens(value);
    }
    if (Array.isArray(value)) {
      return value.map((item) => this.restoreNode(item));
    }
    if (value && typeof value === "object") {
      const output: Record<string, unknown> = {};
      for (const [key, item] of Object.entries(
        value as Record<string, unknown>,
      )) {
        const restoredKey = this.vault.replaceTokens(key);
        setOwn(output, restoredKey, this.restoreNode(item));
      }
      return output;
    }
    return value;
  }

  private snapshotAudit(start: number): AuditEntry[] {
    return this.vault
      .entriesFrom(start)
      .map((entry) => Object.freeze(entry) as AuditEntry);
  }
}

export class SerializationResult {
  readonly body: string;
  readonly audit: readonly AuditEntry[];
  readonly vault: AuditVault;

  constructor(
    body: string,
    audit: readonly AuditEntry[],
    vault: AuditVault,
  ) {
    this.body = body;
    this.audit = Object.freeze([...audit]);
    this.vault = vault;
  }

  toString(): string {
    return this.body;
  }

  get payload(): string {
    return this.body;
  }

  get serialized(): string {
    return this.body;
  }

  get mapping(): AuditVault {
    return this.vault;
  }

  /** Never include the reversible audit mapping in a response body. */
  toJSON(): { body: string } {
    return { body: this.body };
  }
}

export function createDesensitizer(
  options?: DesensitizerOptions,
): Desensitizer {
  return new Desensitizer(options);
}

export function maskResponse(
  value: unknown,
  options?: DesensitizerOptions,
): SerializationResult {
  return new Desensitizer(options).serializeWithAudit(value);
}

export function serializeResponse(
  value: unknown,
  options?: DesensitizerOptions,
): string {
  return new Desensitizer(options).serialize(value);
}

export function desensitize(
  value: unknown,
  options?: DesensitizerOptions,
): DesensitizeResult {
  const desensitizer = new Desensitizer(options);
  const data = desensitizer.desensitize(value);
  return {
    data,
    audit: desensitizer.getAudit(),
    vault: desensitizer.vault,
  };
}

export function restoreWithVault<T>(value: T, vault: AuditVault): T {
  const restoreNode = (current: unknown): unknown => {
    if (typeof current === "string") {
      if (vault.has(current)) {
        return vault.reveal(current);
      }
      return vault.replaceTokens(current);
    }
    if (Array.isArray(current)) {
      return current.map(restoreNode);
    }
    if (current && typeof current === "object") {
      const output: Record<string, unknown> = {};
      for (const [key, item] of Object.entries(
        current as Record<string, unknown>,
      )) {
        setOwn(
          output,
          vault.replaceTokens(key),
          restoreNode(item),
        );
      }
      return output;
    }
    return current;
  };
  return restoreNode(value) as T;
}

export { deepClone };

export interface DesensitizeResult {
  data: unknown;
  audit: readonly AuditEntry[];
  vault: AuditVault;
}
