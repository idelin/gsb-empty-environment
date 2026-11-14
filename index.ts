import { TemplateError } from './src/errors.ts';
import { mergeOptions, normalizeOptions, type RenderOptions } from './src/options.ts';
import { renderAst, renderTemplateWithOptions } from './src/render.ts';
import { parseTemplateSource, type TemplateAst } from './src/template.ts';

export { TemplateError } from './src/errors.ts';
export type { TemplateErrorCode } from './src/errors.ts';
export type {
  NormalizedLimits,
  RenderLimits,
  RenderOptions,
  TemplateFilter,
} from './src/options.ts';
export type {
  BinaryExpression,
  Expression,
  FilterCall,
  LiteralExpression,
  PathExpression,
  PipelineExpression,
  UnaryExpression,
} from './src/expression.ts';
export type {
  BlockKind,
  BlockNode,
  OutputNode,
  TemplateAst,
  TemplateNode,
  TextNode,
} from './src/template.ts';

export interface RenderConfig extends RenderOptions {
  template: string;
  data?: unknown;
  context?: unknown;
  vars?: unknown;
}

export interface CompiledTemplate {
  (context?: unknown, overrides?: RenderOptions): string;
  readonly ast: TemplateAst;
  render(context?: unknown, overrides?: RenderOptions): string;
}

function isRenderConfig(value: unknown): value is RenderConfig {
  return (
    value !== null &&
    typeof value === 'object' &&
    typeof (value as { template?: unknown }).template === 'string'
  );
}

const OPTION_KEYS = new Set([
  'variables',
  'allowedVariables',
  'declaredVariables',
  'allowedPaths',
  'declaredPaths',
  'whitelist',
  'filters',
  'filterMap',
  'allowedFilters',
  'strictVariables',
  'escapeHtml',
  'limits',
  'maxNodes',
  'maxDepth',
  'maxIterations',
  'maxOutputLength',
  'maxTemplateLength',
  'maxValueNodes',
  'maxValueStringLength',
]);

function adaptOptions(value: unknown): RenderOptions | undefined {
  if (value === undefined || value === null) {
    return undefined;
  }
  if (Array.isArray(value)) {
    return value.every((item) => typeof item === 'string')
      ? { variables: value as string[] }
      : {};
  }
  if (typeof value !== 'object') {
    return {};
  }

  let names: string[];
  try {
    names = Object.getOwnPropertyNames(value);
  } catch {
    return {};
  }
  if (names.length === 0) {
    return {};
  }
  const known = names.some((name) => OPTION_KEYS.has(name));
  if (!known) {
    const allFunctions = names.every((name) => {
      try {
        return typeof (value as Record<string, unknown>)[name] === 'function';
      } catch {
        return false;
      }
    });
    if (allFunctions) {
      return {
        filters: value as Record<string, (...args: unknown[]) => unknown>,
      };
    }
  }
  return value as RenderOptions;
}

function unpack(
  templateOrConfig: string | RenderConfig,
  context: unknown,
  options: RenderOptions | undefined,
): { template: string; context: unknown; options: RenderOptions } {
  if (!isRenderConfig(templateOrConfig)) {
    return {
      template: templateOrConfig,
      context,
      options: adaptOptions(options) ?? {},
    };
  }

  const config = templateOrConfig;
  const data =
    config.context !== undefined
      ? config.context
      : config.data !== undefined
        ? config.data
        : config.vars !== undefined
          ? config.vars
          : context;
  const configOptions: RenderOptions = { ...config };
  delete (configOptions as RenderConfig).template;
  delete (configOptions as RenderConfig).data;
  delete (configOptions as RenderConfig).context;
  delete (configOptions as RenderConfig).vars;

  const adapted = adaptOptions(options);
  return {
    template: config.template,
    context: data,
    options: mergeOptions(configOptions, adapted),
  };
}

/**
 * Compile a template once. Parsing, node counting and syntactic path checks
 * happen at compile time; every render still uses a fresh evaluation context
 * and fresh budget.
 */
export function compileTemplate(
  templateOrConfig: string | RenderConfig,
  options?: RenderOptions,
): CompiledTemplate {
  const unpacked = unpack(templateOrConfig, undefined, options);
  const normalized = normalizeOptions(unpacked.options);
  const ast = parseTemplateSource(unpacked.template, normalized.limits);
  const baseOptions = unpacked.options;
  const baseContext = unpacked.context;

  const execute = (
    context: unknown = baseContext,
    overrides?: RenderOptions,
  ): string => {
    const merged = normalizeOptions(mergeOptions(baseOptions, overrides));
    return renderAst(ast, context, merged);
  };
  const compiled = execute as CompiledTemplate;
  Object.defineProperty(compiled, 'ast', {
    enumerable: true,
    value: ast,
    writable: false,
  });
  Object.defineProperty(compiled, 'render', {
    enumerable: true,
    value: execute,
    writable: false,
  });
  Object.defineProperty(compiled, 'template', {
    enumerable: true,
    value: execute,
    writable: false,
  });
  Object.defineProperty(compiled, 'execute', {
    enumerable: true,
    value: execute,
    writable: false,
  });
  return compiled;
}

/**
 * Render a notification template with the built-in safety model:
 * - context values become frozen, prototype-less views;
 * - only own data properties on literal whitelisted paths are readable;
 * - constructors, prototypes, `__proto__` and calls are rejected;
 * - node, loop, depth and output budgets are enforced;
 * - thrown errors disclose a template position only.
 */
export function renderTemplate(
  templateOrConfig: string | RenderConfig,
  context?: unknown,
  options?: RenderOptions,
): string {
  const unpacked = unpack(templateOrConfig, context, options);
  const normalized = normalizeOptions(unpacked.options);
  return renderTemplateWithOptions(
    unpacked.template,
    unpacked.context,
    normalized,
  );
}

export const render = renderTemplate;
export const renderString = renderTemplate;
export const renderNotificationTemplate = renderTemplate;
export const compile = compileTemplate;

export type RenderAttempt =
  | { ok: true; value: string }
  | {
      ok: false;
      error: {
        code: string;
        line: number;
        column: number;
        offset: number;
      };
    };

export function tryRenderTemplate(
  templateOrConfig: string | RenderConfig,
  context?: unknown,
  options?: RenderOptions,
): RenderAttempt {
  try {
    return {
      ok: true,
      value: renderTemplate(templateOrConfig, context, options),
    };
  } catch (error) {
    if (error instanceof TemplateError) {
      return {
        ok: false,
        error: {
          code: error.code,
          line: error.line,
          column: error.column,
          offset: error.offset,
        },
      };
    }
    return {
      ok: false,
      error: {
        code: 'TEMPLATE_ERROR',
        line: 1,
        column: 1,
        offset: 0,
      },
    };
  }
}

export const renderSafe = tryRenderTemplate;
export const safeRenderTemplate = tryRenderTemplate;
export const renderTemplateSafe = tryRenderTemplate;

export class TemplateRenderer {
  readonly options: RenderOptions;

  constructor(options: RenderOptions = {}) {
    this.options = options ?? {};
  }

  compile(
    templateOrConfig: string | RenderConfig,
    overrides?: RenderOptions,
  ): CompiledTemplate {
    return compileTemplate(
      templateOrConfig,
      mergeOptions(this.options, overrides),
    );
  }

  render(
    templateOrConfig: string | RenderConfig,
    context?: unknown,
    overrides?: RenderOptions,
  ): string {
    return renderTemplate(
      templateOrConfig,
      context,
      mergeOptions(this.options, overrides),
    );
  }

  renderTemplate(
    templateOrConfig: string | RenderConfig,
    context?: unknown,
    overrides?: RenderOptions,
  ): string {
    return this.render(templateOrConfig, context, overrides);
  }

  compileTemplate(
    templateOrConfig: string | RenderConfig,
    overrides?: RenderOptions,
  ): CompiledTemplate {
    return this.compile(templateOrConfig, overrides);
  }
}

export { TemplateRenderer as TemplateEngine };
export { TemplateRenderer as Renderer };
export { TemplateRenderer as TemplateSandbox };

export function createRenderer(options: RenderOptions = {}): TemplateRenderer {
  return new TemplateRenderer(options);
}

export const createTemplateRenderer = createRenderer;
export const createTemplateEngine = createRenderer;
export const createTemplateSandbox = createRenderer;

const api = {
  TemplateError,
  TemplateRenderer,
  compileTemplate,
  createRenderer,
  render,
  renderTemplate,
};

export default api;
