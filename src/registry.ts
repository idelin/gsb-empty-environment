import type { ModuleSet } from './policy.ts';

export class RegistryError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RegistryError';
  }
}

function validate(moduleSet: ModuleSet): void {
  const { modules, root } = moduleSet;
  if (!(root in modules)) throw new RegistryError(`root module "${root}" not found`);
  for (const [id, m] of Object.entries(modules)) {
    for (const ref of m.moduleRefs ?? []) {
      if (!(ref in modules)) throw new RegistryError(`module "${id}" references unknown module "${ref}"`);
    }
  }
  const visiting = new Set<string>();
  const done = new Set<string>();
  const visit = (id: string, path: string[]): void => {
    if (done.has(id)) return;
    if (visiting.has(id)) {
      throw new RegistryError(`module ring detected: ${[...path, id].join(' -> ')}`);
    }
    visiting.add(id);
    for (const ref of modules[id].moduleRefs ?? []) visit(ref, [...path, id]);
    visiting.delete(id);
    done.add(id);
  };
  visit(root, []);
}

export class ModuleRegistry {
  private versions: ModuleSet[] = [];

  constructor(initial: ModuleSet) {
    validate(initial);
    this.versions.push(initial);
  }

  get currentVersion(): number {
    return this.versions.length - 1;
  }

  get current(): ModuleSet {
    return this.versions[this.versions.length - 1];
  }

  update(next: ModuleSet): number {
    validate(next);
    this.versions.push(next);
    return this.currentVersion;
  }

  at(version: number): ModuleSet {
    const ms = this.versions[version];
    if (!ms) throw new RegistryError(`unknown module version: ${version}`);
    return ms;
  }
}
