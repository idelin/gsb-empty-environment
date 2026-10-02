import { DecodeError } from './wire/errors.ts';
import { BufferReader } from './wire/reader.ts';
import { CompiledSchema, decodeMessage, DEFAULT_MAX_DEPTH } from './reflect/decoder.ts';
import { validate, type ValidationResult } from './reflect/validator.ts';
import type { MessageDescriptor } from './reflect/schema.ts';
import { MetricsRegistry } from './metrics/registry.ts';
import { exportJson, exportText } from './metrics/exporter.ts';

export interface DecodeOutcome {
  ok: boolean;
  value?: Record<string, unknown>;
  validation?: ValidationResult;
  error?: DecodeError;
}

/**
 * Reflection-mode protobuf engine with fine-grained metrics:
 *  - decode latency histograms (hrtime.bigint, ns precision)
 *  - schema-compile cache hit rate
 *  - per-error-code counters
 *  - internal state gauges (active decodes, decoded messages, bytes)
 */
export class ProtoEngine {
  readonly metrics = new MetricsRegistry();
  private readonly schemaCache = new WeakMap<MessageDescriptor, CompiledSchema>();
  private readonly maxDepth: number;

  constructor(options: { maxDepth?: number } = {}) {
    this.maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
  }

  private compiled(descriptor: MessageDescriptor): CompiledSchema {
    const cached = this.schemaCache.get(descriptor);
    if (cached) {
      this.metrics.recordHit('schema_cache');
      return cached;
    }
    this.metrics.recordMiss('schema_cache');
    const built = new CompiledSchema(descriptor);
    this.schemaCache.set(descriptor, built);
    return built;
  }

  /** Decode + validate. Never throws: failures are reported on the outcome. */
  decode(descriptor: MessageDescriptor, bytes: Uint8Array): DecodeOutcome {
    this.metrics.incGauge('decode_active');
    const stop = this.metrics.startTimer('decode_latency');
    const reader = new BufferReader(bytes);
    try {
      const schema = this.compiled(descriptor);
      const value = decodeMessage(reader, schema, {
        maxDepth: this.maxDepth,
        onUnknownField: () => this.metrics.incCounter('decode_unknown_fields'),
      }, 0);
      this.metrics.incCounter('decode_messages_total');
      this.metrics.incCounter('decode_bytes_total', bytes.byteLength);
      const vStop = this.metrics.startTimer('validate_latency');
      const validation = validate(value, descriptor, this.maxDepth);
      vStop();
      if (!validation.valid) {
        this.metrics.incCounter('validate_failures_total');
        for (const issue of validation.issues) {
          this.metrics.incCounter(`validate_issue_${issue.rule}`);
        }
      }
      return { ok: validation.valid, value, validation };
    } catch (err) {
      const code = err instanceof DecodeError ? err.code : 'ERR_TRUNCATED';
      this.metrics.incCounter('decode_errors_total');
      this.metrics.incCounter(`decode_error_${code}`);
      return { ok: false, error: err instanceof DecodeError ? err : new DecodeError('ERR_TRUNCATED', String(err), 0) };
    } finally {
      stop();
      this.metrics.incGauge('decode_active', -1);
      reader.release();
    }
  }

  exportMetrics(): ReturnType<typeof exportJson> {
    return exportJson(this.metrics);
  }

  exportMetricsText(): string {
    return exportText(this.metrics);
  }
}
