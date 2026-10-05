export interface PluginManifest {
  name: string;
  version: string;
  requires?: Record<string, string>;
  optional?: Record<string, string>;
  conflicts?: Record<string, string>;
}

export interface PluginContext {
  readonly pluginName: string;
  get(name: string): unknown;
  require(name: string): unknown;
}

export interface PluginModule {
  manifest: PluginManifest;
  init(ctx: PluginContext): unknown;
  dispose?(): void;
}

export type Registry = Map<string, PluginModule[]>;
