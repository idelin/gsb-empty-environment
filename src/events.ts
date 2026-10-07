export type RegistryEvent =
  | { type: 'command-registered'; id: string; priority: number; when: string }
  | { type: 'command-unregistered'; id: string }
  | { type: 'context-set'; key: string; value: boolean }
  | { type: 'context-unset'; key: string };
