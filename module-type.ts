export type ModuleDestination = "module" | "script";

export function isModuleTypeAllowed(
  moduleType: string,
  destination: ModuleDestination,
): boolean {
  if (moduleType === "") {
    return true;
  }
  if (moduleType === "json") {
    return destination === "module";
  }
  return false;
}
