import type { SourceRef } from './origin.ts';

export type ConfigErrorCode =
  | 'DUPLICATE_KEY'
  | 'NULL_VALUE'
  | 'TYPE_CONVERSION'
  | 'TYPE_MISMATCH'
  | 'UNKNOWN_KEY'
  | 'INVALID_LIST_OP'
  | 'PARSE_ERROR';

export class ConfigError extends Error {
  readonly code: ConfigErrorCode;
  /** Dotted config path, e.g. "db.password". */
  readonly path: string;
  /** Where the offending value came from. */
  readonly source?: SourceRef;
  readonly details?: Record<string, unknown>;

  constructor(
    code: ConfigErrorCode,
    message: string,
    opts: { path?: string; source?: SourceRef; details?: Record<string, unknown> } = {},
  ) {
    super(formatMessage(code, message, opts.path, opts.source));
    this.name = 'ConfigError';
    this.code = code;
    this.path = opts.path ?? '';
    this.source = opts.source;
    this.details = opts.details;
  }
}

export function formatSource(source: SourceRef): string {
  const loc = source.location;
  const at = loc ? `:${loc.line}:${loc.column}` : '';
  return `${source.kind}:${source.name}${at}`;
}

function formatMessage(
  code: string,
  message: string,
  path: string | undefined,
  source: SourceRef | undefined,
): string {
  const parts = [`[${code}]`, message];
  if (path) parts.push(`(path: ${path})`);
  if (source) parts.push(`(at ${formatSource(source)})`);
  return parts.join(' ');
}
