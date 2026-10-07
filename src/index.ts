export { CommandRegistry } from './registry.ts';
export type {
  AvailabilityList,
  CommandHandler,
  CommandSpec,
  ExecuteOptions,
  ListedCommand,
  ReplayOptions,
} from './registry.ts';
export { ContextStore } from './context.ts';
export { collectKeys, evaluateExpression, parseExpression, strictResolver } from './expression.ts';
export type { ExprNode } from './expression.ts';
export type { RegistryEvent } from './events.ts';
export {
  CommandNotFoundError,
  CommandUnavailableError,
  ContextResolutionError,
  DuplicateCommandError,
  ExpressionSyntaxError,
  StaleContextError,
  UnknownContextKeyError,
} from './errors.ts';
