// Minimal Node globals (no @types/node available offline).
declare var process: {
  argv: string[];
  exitCode: number | undefined;
  env: Record<string, string | undefined>;
};
