import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { Buffer } from "node:buffer";
import type { ClientRequest, RequestOptions } from "node:http";

import { NodeHttpFetcher, type HttpRequestFn } from "../src/http-fetch.ts";
import { parseHttpUrl } from "../src/url-policy.ts";
import {
  RedirectHostMismatchError,
  XmlTooLargeError,
  HttpStatusError,
} from "../src/errors.ts";

interface FakeResponse extends EventEmitter {
  statusCode: number;
  headers: Record<string, string | string[]>;
  destroy: () => void;
  resume: () => void;
  bodyChunks?: Buffer[];
  endAfterEmit?: boolean;
}

function makeResponse(
  statusCode: number,
  headers: Record<string, string | string[]> = {},
): FakeResponse {
  const response = new EventEmitter() as FakeResponse;
  response.statusCode = statusCode;
  response.headers = headers;
  response.destroy = () => {};
  response.resume = () => {};
  return response;
}

function makeRequestFn(responses: FakeResponse[]) {
  const requests: Array<Record<string, unknown>> = [];
  const fakeRequest = new EventEmitter() as EventEmitter & {
    destroy: () => void;
  };
  fakeRequest.destroy = () => {};

  const requestFn: HttpRequestFn = ((
    options: RequestOptions,
    callback: (response: FakeResponse) => void,
  ) => {
    requests.push(options as Record<string, unknown>);
    queueMicrotask(() => {
      const response = responses.shift()!;
      callback(response);
      queueMicrotask(() => {
        for (const chunk of response.bodyChunks ?? []) {
          response.emit("data", chunk);
        }
        if (response.endAfterEmit) {
          response.emit("end");
        }
      });
    });
    return fakeRequest as ClientRequest;
  }) as HttpRequestFn;
  return { requestFn, requests };
}

function responseWithBody(
  body: string | Buffer,
  headers: Record<string, string | string[]> = {},
): FakeResponse {
  const response = makeResponse(200, headers);
  response.bodyChunks = [
    typeof body === "string" ? Buffer.from(body) : body,
  ];
  response.endAfterEmit = true;
  return response;
}

describe("NodeHttpFetcher", () => {
  it("rejects a cross-host redirect without opening a second connection", async () => {
    const redirect = makeResponse(302, {
      location: "http://192.168.1.20/evil.xml",
    });
    const { requestFn, requests } = makeRequestFn([redirect]);
    const fetcher = new NodeHttpFetcher(requestFn);

    await assert.rejects(
      () =>
        fetcher.get(
          parseHttpUrl("http://192.168.1.10/rootDesc.xml"),
          "192.168.1.10",
          { maxBytes: 1024 },
        ),
      RedirectHostMismatchError,
    );
    assert.equal(requests.length, 1);
    assert.equal((requests[0].hostname as string), "192.168.1.10");
  });

  it("follows a same-host relative redirect and returns the body", async () => {
    const redirect = makeResponse(301, { location: "/new/desc.xml" });
    const finalResponse = responseWithBody("<root/>", {
      "content-type": "text/xml",
    });
    const { requestFn, requests } = makeRequestFn([
      redirect,
      finalResponse,
    ]);
    const fetcher = new NodeHttpFetcher(requestFn);

    const result = await fetcher.get(
      parseHttpUrl("http://192.168.1.10/old.xml"),
      "192.168.1.10",
      { maxBytes: 1024 },
    );
    assert.equal(result.url, "http://192.168.1.10/new/desc.xml");
    assert.equal(result.body.toString(), "<root/>");
    assert.equal(requests.length, 2);
    assert.equal(requests[1].path, "/new/desc.xml");
  });

  it("aborts when the streamed body exceeds the size cap", async () => {
    const response = responseWithBody(Buffer.from("abcdefgh"));
    const { requestFn } = makeRequestFn([response]);
    const fetcher = new NodeHttpFetcher(requestFn);

    const promise = fetcher.get(
      parseHttpUrl("http://192.168.1.10/big.xml"),
      "192.168.1.10",
      { maxBytes: 4 },
    );

    await assert.rejects(promise, XmlTooLargeError);
  });

  it("rejects based on Content-Length before reading the body", async () => {
    const response = makeResponse(200, { "content-length": "99999" });
    const { requestFn } = makeRequestFn([response]);
    const fetcher = new NodeHttpFetcher(requestFn);

    await assert.rejects(
      () =>
        fetcher.get(
          parseHttpUrl("http://192.168.1.10/big.xml"),
          "192.168.1.10",
          { maxBytes: 1024 },
        ),
      XmlTooLargeError,
    );
  });

  it("surfaces non-2xx status codes", async () => {
    const response = makeResponse(404);
    response.endAfterEmit = true;
    const { requestFn } = makeRequestFn([response]);
    const fetcher = new NodeHttpFetcher(requestFn);

    const promise = fetcher.get(
      parseHttpUrl("http://192.168.1.10/missing.xml"),
      "192.168.1.10",
      { maxBytes: 1024 },
    );
    await assert.rejects(promise, HttpStatusError);
  });
});
