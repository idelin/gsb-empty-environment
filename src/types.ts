export interface ImportMapEntries {
  readonly [specifier: string]: string;
}

export interface ImportMap {
  readonly imports?: ImportMapEntries;
  readonly scopes?: Record<string, ImportMapEntries>;
}

export interface ImportMapGuardOptions {
  readonly allowedOrigins: readonly string[];
}

interface ValidatedMapping {
  readonly key: string;
  readonly targetHref: string;
  readonly prefix: boolean;
}

export interface ValidatedScope {
  readonly prefix: string;
  readonly mappings: readonly ValidatedMapping[];
}

export interface ValidatedImportMap {
  readonly imports: readonly ValidatedMapping[];
  readonly scopes: readonly ValidatedScope[];
}
