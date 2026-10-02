/**
 * Self-contained metrics exporter.
 *
 * `MetricsHandler` is the transport-independent core: given a URL it returns
 * a fully rendered response (Prometheus text for /metrics, JSON for
 * /snapshot). `MetricsExporter` is a thin node:http adapter around it, so
 * the exposition logic is testable without binding a socket.
 */
import { createServer } from "node:http";
import type { Server, ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { renderPrometheus } from "./prometheus.ts";
import type { RenderOptions } from "./prometheus.ts";
import type { RateLimiterMetrics } from "./metrics.ts";

export interface ExporterOptions extends RenderOptions {
  port?: number;
  host?: string;
}

export interface ExporterResponse {
  status: number;
  contentType: string;
  body: string;
}

export class MetricsHandler {
  readonly #metrics: RateLimiterMetrics;
  readonly #options: RenderOptions;

  constructor(metrics: RateLimiterMetrics, options: RenderOptions = {}) {
    this.#metrics = metrics;
    this.#options = options;
  }

  /** The /metrics endpoint: one snapshot per scrape => consistent output. */
  handle(url: string): ExporterResponse {
    if (url === "/metrics") {
      return {
        status: 200,
        contentType: "text/plain; version=0.0.4; charset=utf-8",
        body: renderPrometheus(this.#metrics, this.#metrics.snapshot(), this.#options),
      };
    }
    if (url === "/snapshot") {
      return {
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(this.#metrics.snapshot()),
      };
    }
    if (url === "/healthz") {
      return { status: 200, contentType: "text/plain", body: "ok" };
    }
    return { status: 404, contentType: "text/plain", body: "not found" };
  }
}

export class MetricsExporter {
  readonly handler: MetricsHandler;
  readonly #options: ExporterOptions;
  #server: Server | null = null;

  constructor(metrics: RateLimiterMetrics, options: ExporterOptions = {}) {
    this.handler = new MetricsHandler(metrics, options);
    this.#options = options;
  }

  async start(): Promise<{ host: string; port: number }> {
    if (this.#server) throw new Error("exporter already started");
    const server = createServer((req, res) => {
      const { status, contentType, body } = this.handler.handle(req.url ?? "/");
      res.writeHead(status, { "content-type": contentType });
      res.end(body);
    });
    this.#server = server;
    const host = this.#options.host ?? "127.0.0.1";
    const port = this.#options.port ?? 0;
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(port, host, () => resolve());
    });
    const addr = server.address() as AddressInfo;
    return { host, port: addr.port };
  }

  async stop(): Promise<void> {
    const server = this.#server;
    if (!server) return;
    this.#server = null;
    await new Promise<void>((resolve, reject) => {
      server.close((err) => (err ? reject(err) : resolve()));
    });
  }
}
