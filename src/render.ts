import { TemplateError } from './errors.ts';
import type { Expression, FilterCall, PathExpression } from './expression.ts';
import { getBuiltinFilter } from './filters.ts';
import type { SourceLocation } from './location.ts';
import {
  normalizeOptions,
  pathIsAllowed,
  type NormalizedOptions,
  type RenderOptions,
  type TemplateFilter,
} from './options.ts';
import {
  MISSING,
  UNSAFE,
  arrayLength,
  box,
  enumerableEntries,
  isArrayView,
  isObjectView,
  isPrimitiveView,
  isTruthy,
  isUnsupportedView,
  readProperty,
  toFilterValue,
  toJson,
  toPrimitive,
  type SafeRead,
  type SafeValue,
} from './safe.ts';
import {
  parseTemplateSource,
  type BlockNode,
  type TemplateAst,
  type TemplateNode,
} from './template.ts';

interface Binding {
  value: SafeValue;
  canonical: string[] | null;
}

interface LoopMeta {
  index: number;
  key: string;
  first: boolean;
  last: boolean;
}

interface Frame {
  value: SafeValue;
  canonical: string[] | null;
  parent: Frame | null;
  bindings: Map<string, Binding>;
  meta: LoopMeta | null;
}

interface EvalResult {
  value: SafeRead;
  canonical: string[] | null;
  sanitized?: boolean;
}

interface BaseResult {
  value: SafeRead;
  canonical: string[] | null;
  fromRoot: boolean;
}

interface RenderState {
  options: NormalizedOptions;
  root: Frame;
  iterations: number;
}

class OutputBuffer {
  private readonly parts: string[] = [];
  private length = 0;
  private readonly maximum: number;

  constructor(maximum: number) {
    this.maximum = maximum;
  }

  append(text: string, location: SourceLocation): void {
    if (text.length > this.maximum - this.length) {
      throw new TemplateError(location, 'BUDGET_EXCEEDED');
    }
    this.parts.push(text);
    this.length += text.length;
  }

  value(): string {
    return this.parts.join('');
  }
}

function copyOwnData(
  target: Record<string, unknown>,
  source: unknown,
  allowed: Set<string> | null,
): void {
  if (source === null || typeof source !== 'object') {
    return;
  }
  let names: string[];
  try {
    names = Object.getOwnPropertyNames(source);
  } catch {
    return;
  }
  for (const name of names) {
    if (allowed !== null && !allowed.has(name)) {
      continue;
    }
    if (name === '__proto__' || name === 'prototype' || name === 'constructor') {
      continue;
    }
    const descriptor = (() => {
      try {
        return Object.getOwnPropertyDescriptor(source, name);
      } catch {
        return undefined;
      }
    })();
    if (
      descriptor !== undefined &&
      descriptor.enumerable === true &&
      'value' in descriptor
    ) {
      target[name] = descriptor.value;
    }
  }
}

function createRootFrame(
  context: unknown,
  options: NormalizedOptions,
): Frame {
  const merged: Record<string, unknown> = Object.create(null);
  copyOwnData(merged, options.defaults, options.declaredVariables);
  copyOwnData(merged, context, options.declaredVariables);

  let rootValue: SafeValue;
  if (options.declaredVariables !== null) {
    rootValue = box(merged);
  } else if (
    context !== null &&
    typeof context === 'object' &&
    !Array.isArray(context)
  ) {
    rootValue = box(merged);
  } else if (context !== undefined && context !== null) {
    rootValue = box(context);
  } else {
    rootValue = box(merged);
  }

  const frame: Frame = {
    value: rootValue,
    canonical: [],
    parent: null,
    bindings: new Map(),
    meta: null,
  };
  return frame;
}

function framesForPath(
  expression: PathExpression,
  frame: Frame,
): Frame | null {
  let current = frame;
  for (let step = 0; step < expression.parentSteps; step += 1) {
    if (current.parent === null) {
      return null;
    }
    current = current.parent;
  }
  return current;
}

function baseForPath(
  expression: PathExpression,
  frame: Frame,
  state: RenderState,
): BaseResult {
  const start = framesForPath(expression, frame);
  if (start === null) {
    return {
      value: MISSING,
      canonical: expression.parentSteps === 0 ? frame.canonical : null,
      fromRoot: false,
    };
  }

  if (expression.root === 'this') {
    return {
      value: start.value,
      canonical: start.canonical,
      fromRoot: false,
    };
  }

  if (expression.root === '@root') {
    return {
      value: state.root.value,
      canonical: [],
      fromRoot: true,
    };
  }

  if (expression.root === '@index' || expression.root === '@first' ||
      expression.root === '@last' || expression.root === '@key') {
    const kind = expression.root.slice(1) as keyof LoopMeta;
    let cursor: Frame | null = start;
    while (cursor !== null) {
      if (cursor.meta !== null) {
        return {
          value: box(cursor.meta[kind]),
          canonical: null,
          fromRoot: false,
        };
      }
      cursor = cursor.parent;
    }
    return { value: MISSING, canonical: null, fromRoot: false };
  }

  let current: Frame | null = start;
  while (current !== null) {
    const binding = current.bindings.get(expression.root);
    if (binding !== undefined) {
      return {
        value: binding.value,
        canonical: binding.canonical,
        fromRoot: false,
      };
    }

    const property = readProperty(current.value, expression.root);
    if (property === UNSAFE) {
      return {
        value: UNSAFE,
        canonical: null,
        fromRoot: current === state.root,
      };
    }
    if (property !== MISSING) {
      return {
        value: property,
        canonical:
          current.canonical === null
            ? null
            : [...current.canonical, expression.root],
        fromRoot: current === state.root,
      };
    }
    current = current.parent;
  }

  return {
    value: MISSING,
    canonical:
      start.canonical === null ? null : [...start.canonical, expression.root],
    fromRoot: start === state.root,
  };
}

function evalPath(
  expression: PathExpression,
  frame: Frame,
  state: RenderState,
): EvalResult {
  const base = baseForPath(expression, frame, state);
  if (
    base.fromRoot &&
    !expression.root.startsWith('@') &&
    expression.root !== 'this'
  ) {
    const declared = state.options.declaredVariables;
    if (declared !== null && !declared.has(expression.root)) {
      throw new TemplateError(expression.location, 'UNDECLARED_VARIABLE');
    }
  }
  if (base.value === MISSING || base.value === UNSAFE) {
    return { value: base.value, canonical: base.canonical };
  }

  let value: SafeValue = base.value;
  let canonical = base.canonical;
  for (const segment of expression.segments) {
    const property = readProperty(value, segment);
    if (property === MISSING || property === UNSAFE) {
      return {
        value: property,
        canonical: canonical === null ? null : [...canonical, segment],
      };
    }
    value = property;
    if (canonical !== null) {
      canonical = [...canonical, segment];
    }
  }

  if (
    canonical !== null &&
    !pathIsAllowed(state.options.pathPatterns, canonical)
  ) {
    throw new TemplateError(expression.location, 'FORBIDDEN_ACCESS');
  }

  return { value, canonical };
}

function missingAsValue(value: SafeRead): SafeValue {
  return value === MISSING || value === UNSAFE ? box(undefined) : value;
}

function primitiveOf(value: SafeValue): string | number | boolean | null | undefined {
  if (!isPrimitiveView(value)) return undefined;
  return toPrimitive(value);
}

function compareValues(operator: string, left: SafeRead, right: SafeRead): boolean {
  const leftValue = missingAsValue(left);
  const rightValue = missingAsValue(right);
  const a = primitiveOf(leftValue);
  const b = primitiveOf(rightValue);

  switch (operator) {
    case '===':
      return a === b;
    case '!==':
      return a !== b;
    case '==':
      // Primitive-only loose equality; no object coercion hooks.
      return a === b;
    case '!=':
      return a !== b;
    case '<':
      return (typeof a === 'number' && typeof b === 'number' && a < b) ||
        (typeof a === 'string' && typeof b === 'string' && a < b);
    case '<=':
      return (typeof a === 'number' && typeof b === 'number' && a <= b) ||
        (typeof a === 'string' && typeof b === 'string' && a <= b);
    case '>':
      return (typeof a === 'number' && typeof b === 'number' && a > b) ||
        (typeof a === 'string' && typeof b === 'string' && a > b);
    case '>=':
      return (typeof a === 'number' && typeof b === 'number' && a >= b) ||
        (typeof a === 'string' && typeof b === 'string' && a >= b);
    default:
      return false;
  }
}

function arithmetic(
  operator: string,
  left: SafeRead,
  right: SafeRead,
): SafeValue {
  const a = primitiveOf(missingAsValue(left));
  const b = primitiveOf(missingAsValue(right));

  if (operator === '+') {
    if (typeof a === 'string' || typeof b === 'string') {
      return box(String(a) + String(b));
    }
    if (typeof a === 'number' && typeof b === 'number') {
      return box(a + b);
    }
    return box(undefined);
  }
  if (typeof a !== 'number' || typeof b !== 'number') {
    return box(undefined);
  }
  if (operator === '-') return box(a - b);
  if (operator === '*') return box(a * b);
  if (operator === '/') return box(b === 0 ? Infinity : a / b);
  return box(undefined);
}

function applyFilters(
  initial: EvalResult,
  filters: FilterCall[],
  frame: Frame,
  state: RenderState,
): EvalResult {
  let value = missingAsValue(initial.value);
  let sanitized = false;
  for (const filter of filters) {
    const name = filter.name;
    const allowed = state.options.allowedFilters;
    if (allowed !== null && !allowed.has(name)) {
      throw new TemplateError(filter.location, 'UNKNOWN_FILTER');
    }

    const custom = state.options.filters.get(name);
    const builtin = getBuiltinFilter(name);
    if (custom === undefined && builtin === undefined) {
      throw new TemplateError(filter.location, 'UNKNOWN_FILTER');
    }

    const argResults = filter.arguments.map((argument) =>
      evalExpression(argument, frame, state),
    );
    const args: SafeValue[] = argResults.map((result) =>
      missingAsValue(result.value),
    );

    if (custom !== undefined) {
      let callArgs: unknown[];
      try {
        const pathAllowed =
          sanitized || state.options.pathPatterns === null
            ? undefined
            : (path: readonly string[]) =>
                pathIsAllowed(state.options.pathPatterns ?? null, path);
        const budget = {
          maxLength: state.options.limits.maxValueStringLength,
          maxDepth: state.options.limits.maxDepth,
          maxNodes: state.options.limits.maxValueNodes,
        };
        callArgs = [toFilterValue(
          value,
          budget,
          filter.location,
          sanitized ? [] : initial.canonical ?? [],
          pathAllowed,
        )];
        for (let index = 0; index < args.length; index += 1) {
          callArgs.push(toFilterValue(
            args[index],
            budget,
            filter.location,
            argResults[index].canonical ?? [],
            pathAllowed,
          ));
        }
      } catch (error) {
        if (error instanceof TemplateError) throw error;
        throw new TemplateError(filter.location, 'UNSAFE_VALUE');
      }

      try {
        const result = Reflect.apply(custom as TemplateFilter, undefined, callArgs);
        value = box(result);
      } catch {
        throw new TemplateError(filter.location, 'UNSAFE_VALUE');
      }
    } else if (builtin !== undefined) {
      try {
        value = builtin(value, args, {
          location: filter.location,
          limits: state.options.limits,
          pathPatterns: sanitized ? null : state.options.pathPatterns,
          pathPrefix: sanitized ? [] : initial.canonical ?? [],
        });
      } catch (error) {
        if (error instanceof TemplateError) throw error;
        throw new TemplateError(filter.location, 'UNSAFE_VALUE');
      }
    }

    if (
      name === 'keys' ||
      name === 'json' ||
      name === 'join' ||
      name === 'upper' ||
      name === 'uppercase' ||
      name === 'lower' ||
      name === 'lowercase' ||
      name === 'trim' ||
      name === 'capitalize' ||
      name === 'escape' ||
      name === 'html' ||
      name === 'urlencode' ||
      name === 'length' ||
      name === 'replace' ||
      name === 'truncate' ||
      name === 'bool'
    ) {
      sanitized = true;
    }
  }

  return { value, canonical: initial.canonical, sanitized };
}

function evalExpression(
  expression: Expression,
  frame: Frame,
  state: RenderState,
): EvalResult {
  switch (expression.type) {
    case 'literal':
      return { value: box(expression.value), canonical: null };

    case 'path': {
      const result = evalPath(expression, frame, state);
      if (
        state.options.strictVariables &&
        (result.value === MISSING || result.value === UNSAFE)
      ) {
        throw new TemplateError(expression.location, 'MISSING_VARIABLE');
      }
      return result;
    }

    case 'unary': {
      const argument = evalExpression(expression.argument, frame, state).value;
      if (expression.operator === '!') {
        return { value: box(!isTruthy(missingAsValue(argument))), canonical: null };
      }
      const primitive = primitiveOf(missingAsValue(argument));
      if (typeof primitive === 'number') {
        return {
          value: box(expression.operator === '-' ? -primitive : primitive),
          canonical: null,
        };
      }
      return { value: box(undefined), canonical: null };
    }

    case 'binary': {
      if (expression.operator === '&&' || expression.operator === '||') {
        const left = evalExpression(expression.left, frame, state).value;
        const truthy = isTruthy(missingAsValue(left));
        if (expression.operator === '&&') {
          return truthy
            ? { value: evalExpression(expression.right, frame, state).value, canonical: null }
            : { value: box(false), canonical: null };
        }
        return truthy
          ? { value: left, canonical: null }
          : { value: evalExpression(expression.right, frame, state).value, canonical: null };
      }

      const left = evalExpression(expression.left, frame, state).value;
      const right = evalExpression(expression.right, frame, state).value;
      if (
        expression.operator === '+' ||
        expression.operator === '-' ||
        expression.operator === '*' ||
        expression.operator === '/'
      ) {
        return {
          value: arithmetic(expression.operator, left, right),
          canonical: null,
        };
      }
      return {
        value: box(compareValues(expression.operator, left, right)),
        canonical: null,
      };
    }

    case 'pipeline': {
      const base = evalExpression(expression.value, frame, state);
      return applyFilters(base, expression.filters, frame, state);
    }
  }
}

function escapeText(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function displayString(
  value: SafeRead,
  state: RenderState,
  location: SourceLocation,
  pathPrefix: readonly string[],
  applyPathFilter: boolean,
): string {
  if (value === MISSING || value === UNSAFE) {
    return '';
  }
  const primitive = primitiveOf(value);
  if (isPrimitiveView(value)) {
    if (primitive === null || primitive === undefined) return '';
    let text: string;
    if (typeof primitive === 'number') {
      text = Number.isFinite(primitive) ? String(primitive) : '';
    } else {
      text = String(primitive);
    }
    return state.options.escapeHtml ? escapeText(text) : text;
  }
  if (isUnsupportedView(value)) {
    return '';
  }
  const json = toJson(value, {
    maxLength: state.options.limits.maxValueStringLength,
    maxDepth: state.options.limits.maxDepth,
    maxNodes: state.options.limits.maxValueNodes,
    pathAllowed:
      !applyPathFilter || state.options.pathPatterns === null
        ? undefined
        : (path) => pathIsAllowed(state.options.pathPatterns ?? null, path),
  }, location, pathPrefix);
  return state.options.escapeHtml ? escapeText(json) : json;
}

function consumeIteration(state: RenderState, location: SourceLocation): void {
  state.iterations += 1;
  if (state.iterations > state.options.limits.maxIterations) {
    throw new TemplateError(location, 'BUDGET_EXCEEDED');
  }
}

function bindAlias(
  bindings: Map<string, Binding>,
  alias: string | null,
  value: SafeValue,
  canonical: string[] | null,
): void {
  if (alias !== null) {
    bindings.set(alias, { value, canonical });
  }
}

function childFrame(
  parent: Frame,
  value: SafeValue,
  canonical: string[] | null,
  meta: LoopMeta | null,
  alias: string | null,
): Frame {
  const frame: Frame = {
    value,
    canonical,
    parent,
    bindings: new Map(),
    meta,
  };
  bindAlias(frame.bindings, alias, value, canonical);
  return frame;
}

function renderEach(
  node: BlockNode,
  frame: Frame,
  state: RenderState,
  output: OutputBuffer,
): void {
  const evaluated = evalExpression(node.expression, frame, state);
  const value = evaluated.value;
  if (value === MISSING || value === UNSAFE) {
    renderNodes(node.alternate, frame, state, output);
    return;
  }

  if (isArrayView(value)) {
    const length = arrayLength(value) ?? 0;
    if (length === 0) {
      renderNodes(node.alternate, frame, state, output);
      return;
    }
    for (let index = 0; index < length; index += 1) {
      consumeIteration(state, node.location);
      const property = readProperty(value, String(index));
      const item = property === MISSING || property === UNSAFE
        ? box(undefined)
        : property;
      const canonical = evaluated.canonical === null
        ? null
        : [...evaluated.canonical, String(index)];
      const child = childFrame(frame, item, canonical, {
        index,
        key: String(index),
        first: index === 0,
        last: index === length - 1,
      }, node.alias);
      renderNodes(node.body, child, state, output);
    }
    return;
  }

  if (isObjectView(value)) {
    const remaining = Math.max(
      0,
      state.options.limits.maxIterations - state.iterations,
    );
    const entries = enumerableEntries(value, remaining + 1);
    if (entries.length > remaining) {
      throw new TemplateError(node.location, 'BUDGET_EXCEEDED');
    }
    if (entries.length === 0) {
      renderNodes(node.alternate, frame, state, output);
      return;
    }
    for (let index = 0; index < entries.length; index += 1) {
      consumeIteration(state, node.location);
      const entry = entries[index];
      const canonical = evaluated.canonical === null
        ? null
        : [...evaluated.canonical, entry.key];
      const child = childFrame(frame, entry.value, canonical, {
        index,
        key: entry.key,
        first: index === 0,
        last: index === entries.length - 1,
      }, node.alias);
      renderNodes(node.body, child, state, output);
    }
    return;
  }

  if (isPrimitiveView(value)) {
    const primitive = toPrimitive(value);
    if (typeof primitive === 'string' && primitive.length > 0) {
      for (let index = 0; index < primitive.length; index += 1) {
        consumeIteration(state, node.location);
        const item = box(primitive[index]);
        const canonical = evaluated.canonical === null
          ? null
          : [...evaluated.canonical, String(index)];
        const child = childFrame(frame, item, canonical, {
          index,
          key: String(index),
          first: index === 0,
          last: index === primitive.length - 1,
        }, node.alias);
        renderNodes(node.body, child, state, output);
      }
      return;
    }
  }

  renderNodes(node.alternate, frame, state, output);
}

function renderWith(
  node: BlockNode,
  frame: Frame,
  state: RenderState,
  output: OutputBuffer,
): void {
  const evaluated = evalExpression(node.expression, frame, state);
  if (
    evaluated.value === MISSING ||
    evaluated.value === UNSAFE ||
    !isTruthy(evaluated.value)
  ) {
    renderNodes(node.alternate, frame, state, output);
    return;
  }
  const child = childFrame(
    frame,
    evaluated.value,
    evaluated.canonical,
    null,
    node.alias,
  );
  renderNodes(node.body, child, state, output);
}

function renderBlock(
  node: BlockNode,
  frame: Frame,
  state: RenderState,
  output: OutputBuffer,
): void {
  if (node.type === 'each') {
    renderEach(node, frame, state, output);
    return;
  }
  if (node.type === 'with') {
    renderWith(node, frame, state, output);
    return;
  }

  const evaluated = evalExpression(node.expression, frame, state);
  let truthy =
    evaluated.value !== MISSING &&
    evaluated.value !== UNSAFE &&
    isTruthy(evaluated.value);
  if (node.type === 'unless') truthy = !truthy;
  renderNodes(
    truthy ? node.body : node.alternate,
    frame,
    state,
    output,
  );
}

function renderNodes(
  nodes: TemplateNode[],
  frame: Frame,
  state: RenderState,
  output: OutputBuffer,
): void {
  for (const node of nodes) {
    if (node.type === 'text') {
      output.append(node.value, node.location);
      continue;
    }
    if (node.type === 'output') {
      const evaluated = evalExpression(node.expression, frame, state);
      output.append(
        displayString(
          evaluated.value,
          state,
          node.location,
          evaluated.canonical ?? [],
          evaluated.sanitized !== true,
        ),
        node.location,
      );
      continue;
    }
    renderBlock(node, frame, state, output);
  }
}

export function renderAst(
  ast: TemplateAst,
  context: unknown,
  options: NormalizedOptions,
): string {
  const fallbackLocation: SourceLocation =
    ast.nodes.length > 0
      ? ast.nodes[0].location
      : { offset: 0, line: 1, column: 1 };
  try {
    const root = createRootFrame(context, options);
    const state: RenderState = {
      options,
      root,
      iterations: 0,
    };
    const output = new OutputBuffer(options.limits.maxOutputLength);
    renderNodes(ast.nodes, root, state, output);
    return output.value();
  } catch (error) {
    if (error instanceof TemplateError) throw error;
    throw new TemplateError(fallbackLocation, 'UNSAFE_VALUE');
  }
}

export function renderTemplateWithOptions(
  template: string,
  context: unknown,
  options: NormalizedOptions,
): string {
  const parsed = parseTemplateSource(template, options.limits);
  return renderAst(parsed, context, options);
}

export function normalizedOptions(input: unknown): NormalizedOptions {
  return normalizeOptions(
    input !== null && typeof input === 'object'
      ? (input as RenderOptions)
      : {},
  );
}
