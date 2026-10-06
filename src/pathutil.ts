import { VfsError, ERR } from './errors.ts';

export function isValidName(name: string): boolean {
  return name !== '' && name !== '.' && name !== '..' && !name.includes('/');
}

export function splitPath(path: string): string[] {
  if (typeof path !== 'string' || !path.startsWith('/')) {
    throw new VfsError(ERR.InvalidPath, `path must be absolute: ${path}`);
  }
  return path.split('/').filter((seg) => seg !== '');
}

export function joinPath(segs: string[]): string {
  return '/' + segs.join('/');
}
