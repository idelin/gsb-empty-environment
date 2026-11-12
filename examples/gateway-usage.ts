/**
 * RPC 网关接入示例：先严格解码并校验租户字段，再把规范字节转发给后端。
 * 运行：node --experimental-strip-types examples/gateway-usage.ts
 */
import {
  ThriftDecodeError,
  ThriftType,
  decodeMessage,
  getRequiredField,
  type FieldTable,
} from '../index.ts';

const TENANT_FIELD_ID = 1;

const requestFieldTable: FieldTable = new Map([
  [TENANT_FIELD_ID, { type: ThriftType.I32 }],
  [2, { type: ThriftType.STRING }],
  [3, { type: ThriftType.LIST, element: { type: ThriftType.I64 } }],
]);

export interface GateResult {
  allowed: boolean;
  reason?: string;
  /** 允许转发时，后端只接收这一份规范重编码字节。 */
  forwardBytes?: Uint8Array;
  tenantId?: number;
}

export function gateRequest(rawRequest: Uint8Array): GateResult {
  try {
    const decoded = decodeMessage(rawRequest, requestFieldTable, {
      maxDepth: 16,
      maxUnknownBytes: 64 * 1024,
      maxMessageBytes: 16 * 1024 * 1024,
    });

    const tenantField = getRequiredField(decoded.root, TENANT_FIELD_ID);
    if (tenantField.value.type !== ThriftType.I32) {
      return { allowed: false, reason: 'tenant field has wrong wire type' };
    }

    return {
      allowed: true,
      forwardBytes: decoded.canonical,
      tenantId: tenantField.value.value,
    };
  } catch (error) {
    if (error instanceof ThriftDecodeError) {
      return { allowed: false, reason: `${error.code}@${error.offset}` };
    }
    return { allowed: false, reason: 'unexpected decoder failure' };
  }
}
