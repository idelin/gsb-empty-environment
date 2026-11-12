/**
 * 最小 FedCM 身份提供方（node:http 版）。
 * 运行：node --experimental-strip-types examples/server.ts
 * 路由：
 *   GET  /fedcm/accounts
 *   GET  /fedcm/client_metadata?client_id=...
 *   POST /fedcm/id_assertion
 */

import { createServer } from "node:http";
import { nodeHttpHandler } from "../index.ts";
import {
  accountsHandler,
  clientMetadataHandler,
  idAssertionHandler,
} from "../test/demo-idp.ts";

const port = Number(process.env.PORT ?? 3000);

const server = createServer((req, res) => {
  const route = (req.url ?? "").split("?")[0];

  if (req.method === "GET" && route === "/fedcm/accounts") {
    return nodeHttpHandler(accountsHandler)(req, res);
  }
  if (req.method === "GET" && route === "/fedcm/client_metadata") {
    return nodeHttpHandler(clientMetadataHandler)(req, res);
  }
  if (req.method === "POST" && route === "/fedcm/id_assertion") {
    return nodeHttpHandler(idAssertionHandler)(req, res);
  }

  res.statusCode = 404;
  res.setHeader("content-type", "application/json");
  res.end(JSON.stringify({ error: { reason: "not_found", message: "Unknown route." } }));
});

server.listen(port, "127.0.0.1", () => {
  console.log(`FedCM IdP listening on http://127.0.0.1:${port}`);
});
