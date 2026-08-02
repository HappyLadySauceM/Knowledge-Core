import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import type { IncomingMessage, Server, ServerResponse } from "node:http";
import { createServer } from "node:http";
import { createServer as createSecureServer } from "node:https";
import type { TLSSocket } from "node:tls";

import type { Config } from "../config.js";
import {
  decodeVersionCursor,
  isRecord,
  validateDocumentID,
  validateIdempotencyKey,
  validateLabel,
  validateVersionID,
} from "../domain.js";
import {
  asServiceError,
  internal,
  invalidInput,
  ServiceError,
  unauthenticated,
} from "../errors.js";
import type { Logger } from "../logger.js";
import type { VersionService } from "../version-service.js";

const problemContentType = "application/problem+json; charset=utf-8";
const jsonContentType = "application/json; charset=utf-8";
const requestIDPattern = /^[A-Za-z0-9._-]{1,128}$/;

export interface Readiness {
  ready(): Promise<void>;
  isLive(): boolean;
}

export class InternalServer {
  private readonly server: Server;
  private accepting = false;

  constructor(
    private readonly config: Config["internalServer"],
    private readonly versions: VersionService,
    private readonly readiness: Readiness,
    private readonly logger: Logger,
  ) {
    const listener = (
      request: IncomingMessage,
      response: ServerResponse,
    ): void => {
      void this.handle(request, response);
    };
    this.server = config.tls.enabled
      ? createSecureServer(
          {
            cert: readFileSync(
              requiredTLSPath(config.tls.certFile, "internal TLS certificate"),
            ),
            key: readFileSync(
              requiredTLSPath(config.tls.keyFile, "internal TLS key"),
            ),
            ca: readFileSync(
              requiredTLSPath(config.tls.caFile, "internal client CA"),
            ),
            requestCert: true,
            rejectUnauthorized: true,
            minVersion: "TLSv1.3",
          },
          listener,
        )
      : createServer(listener);
    this.server.requestTimeout = 15_000;
    this.server.headersTimeout = 10_000;
    this.server.keepAliveTimeout = 5_000;
  }

  async listen(): Promise<void> {
    await new Promise<void>((resolve, reject) => {
      const onError = (error: Error): void => reject(error);
      this.server.once("error", onError);
      this.server.listen(this.config.port, this.config.host, () => {
        this.server.off("error", onError);
        this.accepting = true;
        resolve();
      });
    });
  }

  async close(): Promise<void> {
    this.accepting = false;
    if (!this.server.listening) return;
    await new Promise<void>((resolve, reject) => {
      this.server.close((error) =>
        error === undefined ? resolve() : reject(error),
      );
      this.server.closeIdleConnections();
    });
  }

  private async handle(
    request: IncomingMessage,
    response: ServerResponse,
  ): Promise<void> {
    const started = Date.now();
    const requestID = validRequestID(request.headers["x-request-id"]);
    response.setHeader("X-Request-ID", requestID);
    response.setHeader("X-Content-Type-Options", "nosniff");
    response.setHeader("Cache-Control", "no-store");
    let route = "unmatched";
    try {
      if (!this.accepting)
        throw new ServiceError(
          503,
          40_007,
          "collaboration.not_ready",
          "service unavailable",
        );
      if (this.config.tls.enabled && !(request.socket as TLSSocket).authorized)
        throw unauthenticated();
      const url = new URL(request.url ?? "/", "http://collaboration.internal");
      if (url.pathname === "/health/live") {
        route = "/health/live";
        requireMethod(request, "GET");
        if (!this.readiness.isLive())
          throw new ServiceError(
            503,
            40_007,
            "collaboration.not_live",
            "service unavailable",
          );
        writeJSON(response, 200, { status: "ok", service: "collaboration" });
        return;
      }
      if (url.pathname === "/health/ready") {
        route = "/health/ready";
        requireMethod(request, "GET");
        await this.readiness.ready();
        writeJSON(response, 200, { status: "ready", service: "collaboration" });
        return;
      }
      const match =
        /^\/internal\/v1\/documents\/([^/]+)(?:\/versions(?:\/([^/]+)(?:\/restorations)?)?)?$/.exec(
          url.pathname,
        );
      if (match?.[1] === undefined)
        throw new ServiceError(
          404,
          40_004,
          "collaboration.route_not_found",
          "route not found",
        );
      const documentID = validateDocumentID(decodePath(match[1]));
      const versionID =
        match[2] === undefined
          ? undefined
          : validateVersionID(decodePath(match[2]));
      const hasVersions = url.pathname.includes("/versions");
      const restoration = url.pathname.endsWith("/restorations");
      const token = bearerToken(request);
      const idempotencyKey = validateIdempotencyKey(
        singleHeader(request, "idempotency-key"),
      );

      if (!hasVersions) {
        route = "/internal/v1/documents/:document_id";
        requireMethod(request, "DELETE");
        requireNoQuery(url);
        await this.versions.purge(documentID);
        response.writeHead(204).end();
        return;
      }
      if (versionID === undefined && request.method === "GET") {
        route = "/internal/v1/documents/:document_id/versions";
        requireOnlyQuery(url, new Set(["cursor", "limit"]));
        const cursor = decodeVersionCursor(singleQuery(url, "cursor"));
        const limit = parseLimit(singleQuery(url, "limit"));
        const page = await this.versions.list(documentID, token, cursor, limit);
        writeJSON(response, 200, {
          items: page.items,
          page: {
            ...(page.nextCursor === undefined
              ? {}
              : { next_cursor: page.nextCursor }),
            has_more: page.hasMore,
          },
        });
        return;
      }
      if (versionID === undefined) {
        route = "/internal/v1/documents/:document_id/versions";
        requireMethod(request, "POST");
        requireNoQuery(url);
        const body = await readJSONObject(
          request,
          this.config.maxBodyBytes,
          new Set(["label"]),
        );
        const label = validateLabel(body.label);
        writeJSON(
          response,
          201,
          await this.versions.create(documentID, token, label, idempotencyKey),
        );
        return;
      }
      if (!restoration) {
        route = "/internal/v1/documents/:document_id/versions/:version_id";
        requireMethod(request, "GET");
        requireNoQuery(url);
        writeJSON(
          response,
          200,
          await this.versions.get(documentID, versionID, token),
        );
        return;
      }
      route =
        "/internal/v1/documents/:document_id/versions/:version_id/restorations";
      requireMethod(request, "POST");
      requireNoQuery(url);
      const body = await readJSONObject(
        request,
        this.config.maxBodyBytes,
        new Set(["expected_sequence"]),
      );
      const expectedSequence = body.expected_sequence;
      if (
        !Number.isSafeInteger(expectedSequence) ||
        Number(expectedSequence) < 0
      ) {
        throw invalidInput(
          "expected_sequence must be a non-negative safe integer",
        );
      }
      writeJSON(
        response,
        201,
        await this.versions.restore(
          documentID,
          versionID,
          Number(expectedSequence),
          token,
          idempotencyKey,
        ),
      );
    } catch (error) {
      const serviceError = asServiceError(error);
      if (!response.headersSent)
        writeProblem(response, requestID, serviceError);
      else response.destroy();
      if (serviceError.status >= 500)
        this.logger.error("Collaboration internal request failed", error, {
          route,
        });
    } finally {
      this.logger.info("Collaboration internal request completed", {
        component: "collaboration.internal_http",
        route,
        method: request.method ?? "unknown",
        status: response.statusCode,
        duration_ms: Date.now() - started,
      });
    }
  }
}

async function readJSONObject(
  request: IncomingMessage,
  maximumBytes: number,
  allowedKeys: ReadonlySet<string>,
): Promise<Record<string, unknown>> {
  const contentType = request.headers["content-type"];
  if (
    typeof contentType !== "string" ||
    contentType.split(";", 1)[0]?.trim().toLowerCase() !== "application/json"
  ) {
    throw invalidInput("Content-Type must be application/json");
  }
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const raw of request) {
    const chunk = Buffer.isBuffer(raw) ? raw : Buffer.from(raw as Uint8Array);
    size += chunk.byteLength;
    if (size > maximumBytes) throw invalidInput("request body is too large");
    chunks.push(chunk);
  }
  if (size === 0) throw invalidInput("request body is required");
  let value: unknown;
  try {
    value = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  } catch (error) {
    throw invalidInput("request body is not valid JSON", { cause: error });
  }
  if (
    !isRecord(value) ||
    Object.keys(value).some((key) => !allowedKeys.has(key))
  ) {
    throw invalidInput(
      "request body contains unknown fields or is not an object",
    );
  }
  return value;
}

function bearerToken(request: IncomingMessage): string | undefined {
  const value = singleHeader(request, "authorization");
  if (value === undefined) return undefined;
  const match = /^Bearer ([^\s]+)$/i.exec(value);
  if (match?.[1] === undefined || match[1].length > 16_384)
    throw unauthenticated();
  return match[1];
}

function singleHeader(
  request: IncomingMessage,
  name: string,
): string | undefined {
  const value = request.headers[name];
  if (value === undefined) return undefined;
  if (Array.isArray(value) || typeof value !== "string")
    throw invalidInput(`${name} header must occur once`);
  return value;
}

function singleQuery(url: URL, name: string): string | undefined {
  const values = url.searchParams.getAll(name);
  if (values.length > 1)
    throw invalidInput(`${name} query parameter must occur at most once`);
  return values[0];
}

function requireOnlyQuery(url: URL, allowed: ReadonlySet<string>): void {
  for (const name of url.searchParams.keys())
    if (!allowed.has(name))
      throw invalidInput(`unknown query parameter ${name}`);
}

function requireNoQuery(url: URL): void {
  requireOnlyQuery(url, new Set());
}

function parseLimit(value: string | undefined): number {
  if (value === undefined || value === "") return 20;
  if (!/^[1-9][0-9]*$/.test(value))
    throw invalidInput("limit must be an integer between 1 and 100");
  const result = Number(value);
  if (!Number.isSafeInteger(result) || result > 100)
    throw invalidInput("limit must be an integer between 1 and 100");
  return result;
}

function requireMethod(request: IncomingMessage, method: string): void {
  if (request.method !== method)
    throw new ServiceError(
      405,
      40_001,
      "collaboration.method_not_allowed",
      "method not allowed",
    );
}

function decodePath(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch (error) {
    throw invalidInput("path parameter is malformed", { cause: error });
  }
}

function writeJSON(
  response: ServerResponse,
  status: number,
  value: unknown,
): void {
  const body = Buffer.from(JSON.stringify(value));
  response.writeHead(status, {
    "Content-Type": jsonContentType,
    "Content-Length": String(body.byteLength),
  });
  response.end(body);
}

function writeProblem(
  response: ServerResponse,
  requestID: string,
  error: ServiceError,
): void {
  const body = Buffer.from(
    JSON.stringify({
      type: `urn:knowledge-core:problem:${error.key}`,
      title: statusTitle(error.status),
      status: error.status,
      detail: error.detail,
      code: error.code,
      key: error.key,
      request_id: requestID,
    }),
  );
  response.writeHead(error.status, {
    "Content-Type": problemContentType,
    "Content-Length": String(body.byteLength),
  });
  response.end(body);
}

function statusTitle(status: number): string {
  const titles: Record<number, string> = {
    400: "Bad Request",
    401: "Unauthorized",
    403: "Forbidden",
    404: "Not Found",
    405: "Method Not Allowed",
    409: "Conflict",
    412: "Precondition Failed",
    500: "Internal Server Error",
    503: "Service Unavailable",
  };
  return titles[status] ?? "Error";
}

function validRequestID(value: string | string[] | undefined): string {
  return typeof value === "string" && requestIDPattern.test(value)
    ? value
    : randomUUID();
}

function requiredTLSPath(
  value: string | undefined,
  description: string,
): string {
  if (value === undefined)
    throw internal({ cause: new Error(`${description} is required`) });
  return value;
}
