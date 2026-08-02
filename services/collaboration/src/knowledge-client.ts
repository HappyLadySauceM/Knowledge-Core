import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import type {
  Agent as HTTPAgent,
  IncomingMessage,
  RequestOptions,
} from "node:http";
import { Agent as PlainAgent, request as requestHTTP } from "node:http";
import { Agent as TLSAgent, request as requestHTTPS } from "node:https";

import type { Config } from "./config.js";
import type { Access, Authorization, PublicUser } from "./domain.js";
import { isRecord } from "./domain.js";
import {
  forbidden,
  internal,
  invalidInput,
  notFound,
  unauthenticated,
  unavailable,
} from "./errors.js";
import type { ServiceError } from "./errors.js";

const maximumResponseBytes = 1 << 20;

export class KnowledgeClient {
  private readonly agent: HTTPAgent;

  constructor(private readonly config: Config["knowledge"]) {
    this.agent = config.tls.enabled
      ? new TLSAgent({
          keepAlive: true,
          rejectUnauthorized: true,
          ...(config.tls.caFile === undefined
            ? {}
            : { ca: readFileSync(config.tls.caFile) }),
          ...(config.tls.certFile === undefined
            ? {}
            : { cert: readFileSync(config.tls.certFile) }),
          ...(config.tls.keyFile === undefined
            ? {}
            : { key: readFileSync(config.tls.keyFile) }),
        })
      : new PlainAgent({ keepAlive: true });
  }

  async authorize(
    documentID: string,
    token: string | undefined,
  ): Promise<Authorization> {
    const response = await this.request(
      "POST",
      `/internal/v1/documents/${encodeURIComponent(documentID)}/authorization`,
      token,
    );
    if (response.status !== 200)
      throw mapKnowledgeError(response.status, response.body);
    let value: unknown;
    try {
      value = JSON.parse(response.body.toString("utf8"));
    } catch (error) {
      throw unavailable({ cause: error });
    }
    if (!isRecord(value))
      throw unavailable({
        cause: new Error("Knowledge authorization response is not an object"),
      });
    const allowedKeys = new Set([
      "document_id",
      "access",
      "user",
      "token_expires_at",
      "permission_revision",
    ]);
    if (Object.keys(value).some((key) => !allowedKeys.has(key))) {
      throw unavailable({
        cause: new Error(
          "Knowledge authorization response contains unknown fields",
        ),
      });
    }
    if (
      value.document_id !== documentID ||
      !isAccess(value.access) ||
      !Number.isSafeInteger(value.permission_revision) ||
      Number(value.permission_revision) <= 0
    ) {
      throw unavailable({
        cause: new Error("Knowledge authorization response is incomplete"),
      });
    }
    const user = value.user === undefined ? undefined : parseUser(value.user);
    const tokenExpiresAt =
      value.token_expires_at === undefined
        ? undefined
        : parseTimestamp(value.token_expires_at);
    if (
      (token === undefined) !== (user === undefined) ||
      (user !== undefined && tokenExpiresAt === undefined)
    ) {
      throw unavailable({
        cause: new Error(
          "Knowledge authorization identity does not match the bearer token",
        ),
      });
    }
    return {
      documentID,
      access: value.access,
      permissionRevision: Number(value.permission_revision),
      ...(user === undefined ? {} : { user }),
      ...(tokenExpiresAt === undefined ? {} : { tokenExpiresAt }),
    };
  }

  async project(
    documentID: string,
    sequence: number,
    content: Record<string, unknown>,
    plainText: string,
  ): Promise<void> {
    const body = Buffer.from(
      JSON.stringify({ sequence, content, plain_text: plainText }),
    );
    const response = await this.request(
      "PUT",
      `/internal/v1/documents/${encodeURIComponent(documentID)}/projection`,
      undefined,
      body,
    );
    if (response.status !== 204)
      throw mapKnowledgeError(response.status, response.body);
  }

  async ping(): Promise<void> {
    const response = await this.request("GET", "/health/live");
    if (response.status !== 200) throw unavailable();
  }

  close(): void {
    this.agent.destroy();
  }

  private request(
    method: string,
    path: string,
    token?: string,
    body?: Buffer,
  ): Promise<{ status: number; body: Buffer }> {
    const endpoint = new URL(path, this.config.baseURL);
    const options: RequestOptions = {
      protocol: endpoint.protocol,
      hostname: endpoint.hostname,
      port: endpoint.port,
      path: `${endpoint.pathname}${endpoint.search}`,
      method,
      agent: this.agent,
      headers: {
        Accept: "application/json, application/problem+json",
        "X-Request-ID": randomUUID(),
        ...(token === undefined ? {} : { Authorization: `Bearer ${token}` }),
        ...(body === undefined
          ? { "Content-Length": "0" }
          : {
              "Content-Type": "application/json",
              "Content-Length": String(body.byteLength),
            }),
      },
    };
    const requester =
      endpoint.protocol === "https:" ? requestHTTPS : requestHTTP;
    return new Promise((resolve, reject) => {
      const request = requester(options, (response) => {
        void readResponse(response).then(resolve, reject);
      });
      request.setTimeout(this.config.requestTimeoutMs, () => {
        request.destroy(new Error("Knowledge request timed out"));
      });
      request.once("error", (error) => reject(unavailable({ cause: error })));
      if (body !== undefined) request.write(body);
      request.end();
    });
  }
}

async function readResponse(
  response: IncomingMessage,
): Promise<{ status: number; body: Buffer }> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const raw of response) {
    const chunk = Buffer.isBuffer(raw) ? raw : Buffer.from(raw as Uint8Array);
    size += chunk.byteLength;
    if (size > maximumResponseBytes) {
      response.destroy();
      throw unavailable({
        cause: new Error("Knowledge response is too large"),
      });
    }
    chunks.push(chunk);
  }
  return { status: response.statusCode ?? 502, body: Buffer.concat(chunks) };
}

function mapKnowledgeError(status: number, body: Buffer): ServiceError {
  let key = "";
  try {
    const value: unknown = JSON.parse(body.toString("utf8"));
    if (isRecord(value) && typeof value.key === "string") key = value.key;
  } catch {
    return unavailable({
      cause: new Error("Knowledge returned an invalid problem document"),
    });
  }
  if (status === 400 || key === "knowledge.invalid_input")
    return invalidInput();
  if (status === 401 || key === "knowledge.unauthenticated")
    return unauthenticated();
  if (status === 403 || key === "knowledge.forbidden") return forbidden();
  if (
    status === 404 ||
    status === 410 ||
    key === "knowledge.not_found" ||
    key === "knowledge.gone"
  )
    return notFound();
  if (status >= 500) return unavailable();
  return internal({
    cause: new Error(`unexpected Knowledge status ${String(status)}`),
  });
}

function parseUser(value: unknown): PublicUser {
  if (
    !isRecord(value) ||
    Object.keys(value).some(
      (key) => !["id", "username", "avatar"].includes(key),
    ) ||
    !Number.isSafeInteger(value.id) ||
    Number(value.id) <= 0 ||
    typeof value.username !== "string" ||
    value.username === "" ||
    typeof value.avatar !== "string"
  ) {
    throw unavailable({
      cause: new Error("Knowledge authorization user is invalid"),
    });
  }
  return {
    id: Number(value.id),
    username: value.username,
    avatar: value.avatar,
  };
}

function parseTimestamp(value: unknown): Date {
  if (
    typeof value !== "string" ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(value)
  ) {
    throw unavailable({
      cause: new Error("Knowledge token expiration is invalid"),
    });
  }
  const result = new Date(value);
  if (!Number.isFinite(result.getTime()))
    throw unavailable({
      cause: new Error("Knowledge token expiration is invalid"),
    });
  return result;
}

function isAccess(value: unknown): value is Access {
  return value === "viewer" || value === "editor" || value === "owner";
}
