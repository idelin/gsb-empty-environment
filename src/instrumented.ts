/**
 * Instrumented decoder: wraps the reflection decoder with fine-grained
 * metrics — per-message decode latency, decode totals, field hit rate
 * (schema-known vs unknown fields) and error-code tallies.
 */

import { decodeMessage, type DecodeOptions, type DecodeResult, type MessageSchema } from './decoder.ts';
import { DecodeError } from './wire.ts';
import { MetricRegistry } from './metrics.ts';

export class InstrumentedDecoder {
  readonly registry: MetricRegistry;

  constructor(registry: MetricRegistry = new MetricRegistry()) {
    this.registry = registry;
  }

  decode(schema: MessageSchema, data: Uint8Array, options?: DecodeOptions): DecodeResult {
    const labels = { message: schema.name };
    try {
      const result = this.registry.timeSync(
        'decode_latency_ms',
        () => decodeMessage(schema, data, options),
        labels,
      );
      this.registry.counter('decode_total', labels).inc();
      const hr = this.registry.hitRate('field_hit_rate', labels);
      for (let i = 0; i < result.stats.knownFields; i++) hr.hit();
      for (let i = 0; i < result.stats.unknownFields; i++) hr.miss();
      this.registry.gauge('decode_max_depth', labels).set(result.stats.maxDepthReached);
      return result;
    } catch (err) {
      this.registry.counter('decode_error_total', labels).inc();
      if (err instanceof DecodeError) {
        this.registry.errorCodes.record(err.code);
      } else {
        this.registry.errorCodes.record('ERR_UNKNOWN');
      }
      throw err;
    }
  }
}
