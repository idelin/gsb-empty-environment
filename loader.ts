import { isModuleTypeAllowed } from "./module-type.ts";
import type { ModuleDestination } from "./module-type.ts";

export interface ModuleRequest {
  url: string;
  destination: ModuleDestination;
}

export interface LoadedModule {
  url: string;
  destination: ModuleDestination;
  moduleType: string;
  source: string;
}

export function resolveModuleType(url: string): string {
  if (url.endsWith(".json")) {
    return "json";
  }
  return "";
}

export function loadModule(
  request: ModuleRequest,
  source: string,
): LoadedModule {
  const moduleType = resolveModuleType(request.url);
  if (!isModuleTypeAllowed(moduleType, request.destination)) {
    throw new TypeError(
      `Failed to load ${request.url}: module type "${moduleType}" is not allowed for destination "${request.destination}"`,
    );
  }
  return {
    url: request.url,
    destination: request.destination,
    moduleType,
    source,
  };
}
