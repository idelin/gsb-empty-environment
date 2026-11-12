import type { IncomingMessage, ServerResponse } from "node:http";

/**
 * node:http 适配器：把原生 IncomingMessage 聚合成标准 Web `Request`，
 * 使同一套 FedCM 校验函数既能跑在边缘运行时也能跑在 Node HTTP 服务上。
 *
 * 注意：必须在消费 `request.json()` 之前调用（它会读取请求体流）。
 */
export async function toWebRequest(req: IncomingMessage, baseUrl?: string): Promise<Request> {
  const host = req.headers.host ?? "localhost";
  const protocol = baseUrl ?? (host.startsWith("localhost") || host.startsWith("127.0.0.1")
    ? "http"
    : "https");
  const url = `${protocol}://${host}${req.url ?? "/"}`;

  const chunks: Buffer[] = [];
  for await (const chunk of req) {
    chunks.push(chunk as Buffer);
  }
  const body = Buffer.concat(chunks);

  const init: RequestInit & { duplex?: "half" } = {
    method: req.method ?? "GET",
    headers: req.headers as Record<string, string>,
  };
  if (req.method !== undefined && req.method.toUpperCase() !== "GET" && req.method.toUpperCase() !== "HEAD") {
    init.body = body;
    init.duplex = "half";
  }
  return new Request(url, init);
}

/** 把标准 Web `Response` 回写到 node:http 的 ServerResponse。 */
export async function sendWebResponse(res: ServerResponse, response: Response): Promise<void> {
  res.statusCode = response.status;
  response.headers.forEach((value, key) => {
    res.setHeader(key, value);
  });
  const buffer = Buffer.from(await response.arrayBuffer());
  res.end(buffer);
}

/** 便捷组合：用 FedCM 包装处理器直接处理 node:http 请求。 */
export function nodeHttpHandler(
  handler: (request: Request) => Promise<Response> | Response,
): (req: IncomingMessage, res: ServerResponse) => Promise<void> {
  return async (req, res): Promise<void> => {
    try {
      const request = await toWebRequest(req);
      const response = await handler(request);
      await sendWebResponse(res, response);
    } catch (error) {
      res.statusCode = 500;
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify({ error: { reason: "internal_error", message: "Internal server error." } }));
    }
  };
}
