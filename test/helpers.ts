import type { PluginModule, Registry } from "../src/types.ts";

export interface PluginSpec {
  version?: string;
  requires?: Record<string, string>;
  optional?: Record<string, string>;
  conflicts?: Record<string, string>;
  init?: (ctx: { pluginName: string; get(name: string): unknown; require(name: string): unknown }) => unknown;
  dispose?: () => void;
}

export function makePlugin(name: string, spec: PluginSpec = {}): PluginModule {
  return {
    manifest: {
      name,
      version: spec.version ?? "1.0.0",
      requires: spec.requires,
      optional: spec.optional,
      conflicts: spec.conflicts,
    },
    init: (ctx) => (spec.init ? spec.init(ctx) : { name }),
    dispose: spec.dispose,
  };
}

export function makeRegistry(...plugins: PluginModule[]): Registry {
  const registry: Registry = new Map();
  for (const plugin of plugins) {
    const list = registry.get(plugin.manifest.name) ?? [];
    list.push(plugin);
    registry.set(plugin.manifest.name, list);
  }
  return registry;
}
