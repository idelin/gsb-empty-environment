import { createHmac, timingSafeEqual } from 'node:crypto';

/** 继续游标负载：绑定租户、注册表版本与权限代次。 */
export interface CursorPayload {
  v: 1;
  tenant: string;
  registry: number;
  generation: number;
  /** 继续位置的帧序号（相对完整投影计划）。 */
  offset: number;
  /** 完整投影计划的 SHA-256，防止游标被挪到别的图上复用。 */
  plan: string;
}

export class CursorError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CursorError';
  }
}

export interface CursorBinding {
  tenant: string;
  registry: number;
  generation: number;
}

export function signCursor(payload: CursorPayload, key: string): string {
  const body = Buffer.from(JSON.stringify(payload), 'utf8').toString('base64url');
  const mac = createHmac('sha256', key).update(body).digest('base64url');
  return `${body}.${mac}`;
}

/** 校验 HMAC 与租户 / 注册表 / 权限代次绑定；不通过则抛 CursorError。 */
export function verifyCursor(
  token: string,
  key: string,
  binding: CursorBinding,
): CursorPayload {
  const dot = token.lastIndexOf('.');
  if (dot <= 0) throw new CursorError('malformed cursor');
  const body = token.slice(0, dot);
  const mac = Buffer.from(token.slice(dot + 1), 'base64url');
  const expect = createHmac('sha256', key).update(body).digest();
  if (mac.length !== expect.length || !timingSafeEqual(mac, expect)) {
    throw new CursorError('cursor signature mismatch');
  }
  let payload: CursorPayload;
  try {
    payload = JSON.parse(Buffer.from(body, 'base64url').toString('utf8')) as CursorPayload;
  } catch {
    throw new CursorError('cursor payload undecodable');
  }
  if (payload.v !== 1) throw new CursorError('unsupported cursor version');
  if (payload.tenant !== binding.tenant) {
    throw new CursorError('cursor tenant mismatch');
  }
  if (payload.registry !== binding.registry) {
    throw new CursorError('cursor registry mismatch');
  }
  if (payload.generation !== binding.generation) {
    throw new CursorError('cursor generation mismatch');
  }
  return payload;
}
