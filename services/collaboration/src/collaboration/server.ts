import { Redis as RedisExtension } from "@hocuspocus/extension-redis";
import type { Connection, onRequestPayload } from "@hocuspocus/server";
import { Server } from "@hocuspocus/server";

import type { Config } from "../config.js";
import type { Authorization, PublicUser } from "../domain.js";
import { ServiceError } from "../errors.js";
import type { KnowledgeClient } from "../knowledge-client.js";
import type { Logger } from "../logger.js";
import type { CollaborationStore } from "../storage/store.js";
import { DurableUpdateGate, type CollaborationContext } from "./update-gate.js";

interface TokenTimers {
  readonly refresh: NodeJS.Timeout;
  readonly expire: NodeJS.Timeout;
}

export class CollaborationServer {
  readonly redis: RedisExtension;
  private readonly server: Server<CollaborationContext>;
  private readonly updateGate: DurableUpdateGate;
  private readonly tokenTimers = new Map<
    Connection<CollaborationContext>,
    TokenTimers
  >();
  private readonly tokenCloseHooks = new WeakSet<
    Connection<CollaborationContext>
  >();

  constructor(
    private readonly config: Config,
    private readonly store: CollaborationStore,
    private readonly knowledge: KnowledgeClient,
    private readonly logger: Logger,
  ) {
    this.updateGate = new DurableUpdateGate(
      store,
      config.publicServer.maxUpdateBytes,
      config.publicServer.maxDocumentBytes,
    );
    this.redis = redisExtension(config);
    this.server = new Server<CollaborationContext>({
      name: "knowledge-core.collaboration",
      address: config.publicServer.host,
      port: config.publicServer.port,
      stopOnSignals: false,
      quiet: true,
      timeout: 60_000,
      unloadImmediately: true,
      maxUnauthenticatedQueueSize: Math.min(
        config.publicServer.maxUpdateBytes * 2,
        8 << 20,
      ),
      maxUnauthenticatedQueueMessages: 32,
      maxPendingDocuments: 4,
      websocketOptions: {
        maxPayload: config.publicServer.maxUpdateBytes + 16_384,
      },
      extensions: [this.redis],
      onConnect: ({ request, requestHeaders }) => {
        const requestedPath = new URL(request.url).pathname;
        if (requestedPath !== config.publicServer.websocketURL.pathname)
          throw new WebSocketProtocolError(4403, "forbidden");
        const origin = requestHeaders.get("origin");
        if (origin !== null && !config.publicServer.allowedOrigins.has(origin))
          throw new WebSocketProtocolError(4403, "forbidden");
        return Promise.resolve();
      },
      onAuthenticate: async ({ documentName, token, connectionConfig }) => {
        try {
          const authorization = await this.knowledge.authorize(
            documentName,
            normalizeToken(token),
          );
          connectionConfig.readOnly = authorization.access === "viewer";
          return contextFromAuthorization(authorization);
        } catch (error) {
          throw authenticationProtocolError(error);
        }
      },
      onTokenSync: async ({ documentName, token, context, connection }) => {
        try {
          const normalized = normalizeToken(token);
          if (normalized === undefined)
            throw new WebSocketProtocolError(4401, "unauthorized");
          const authorization = await this.knowledge.authorize(
            documentName,
            normalized,
          );
          if (authorization.user?.id !== context.user?.id)
            throw new WebSocketProtocolError(4403, "forbidden");
          const next = contextFromAuthorization(authorization);
          connection.context = next;
          connection.readOnly = authorization.access === "viewer";
          this.scheduleTokenRefresh(connection, next.tokenExpiresAt);
          return next;
        } catch (error) {
          throw authenticationProtocolError(error);
        }
      },
      connected: ({ connection, context }) => {
        this.scheduleTokenRefresh(connection, context.tokenExpiresAt);
        this.logger.info("Collaboration WebSocket connected", {
          component: "collaboration.websocket",
          event: "connected",
          access: context.access,
        });
        return Promise.resolve();
      },
      onDisconnect: () => {
        this.logger.info("Collaboration WebSocket disconnected", {
          component: "collaboration.websocket",
          event: "disconnected",
        });
        return Promise.resolve();
      },
      onLoadDocument: async ({ documentName }) => {
        await this.store.initializeDocument(documentName);
        return (await this.store.loadDocument(documentName)).state;
      },
      beforeSync: (payload) => this.updateGate.beforeSync(payload),
      onRequest: (payload) => this.handlePublicRequest(payload),
    });
  }

  async listen(): Promise<void> {
    await this.server.listen();
  }

  async ready(): Promise<void> {
    await redisPublisher(this.redis).ping();
  }

  closeDocument(documentID: string): void {
    this.server.hocuspocus.closeConnections(documentID);
  }

  async close(): Promise<void> {
    for (const connection of this.tokenTimers.keys())
      this.clearTokenTimers(connection);
    await this.server.destroy();
  }

  private handlePublicRequest({
    request,
    response,
  }: onRequestPayload): Promise<never> {
    const url = new URL(request.url ?? "/", "http://collaboration.public");
    if (request.method === "GET" && url.pathname === "/health/live") {
      const body = Buffer.from('{"status":"ok","service":"collaboration"}');
      response.writeHead(200, {
        "Content-Type": "application/json; charset=utf-8",
        "Content-Length": String(body.byteLength),
        "Cache-Control": "no-store",
        "X-Content-Type-Options": "nosniff",
      });
      response.end(body);
      // Hocuspocus uses a falsy rejection as its documented "response handled"
      // sentinel; an Error would be rethrown by its HTTP adapter.
      // eslint-disable-next-line @typescript-eslint/prefer-promise-reject-errors
      return Promise.reject();
    }
    response.writeHead(404, {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
    });
    response.end("not found");
    // eslint-disable-next-line @typescript-eslint/prefer-promise-reject-errors
    return Promise.reject();
  }

  private scheduleTokenRefresh(
    connection: Connection<CollaborationContext>,
    expiresAt: Date | undefined,
  ): void {
    this.clearTokenTimers(connection);
    if (expiresAt === undefined) return;
    const now = Date.now();
    const refreshDelay = boundedTimerDelay(
      expiresAt.getTime() - now - this.config.publicServer.tokenRefreshLeadMs,
    );
    const expiryDelay = boundedTimerDelay(
      expiresAt.getTime() - now + this.config.publicServer.tokenGraceMs,
    );
    const refresh = setTimeout(() => connection.requestToken(), refreshDelay);
    const expire = setTimeout(() => {
      connection.close({ code: 4401, reason: "token-expired" });
      this.clearTokenTimers(connection);
    }, expiryDelay);
    this.tokenTimers.set(connection, { refresh, expire });
    if (!this.tokenCloseHooks.has(connection)) {
      connection.onClose(() => this.clearTokenTimers(connection));
      this.tokenCloseHooks.add(connection);
    }
  }

  private clearTokenTimers(connection: Connection<CollaborationContext>): void {
    const timers = this.tokenTimers.get(connection);
    if (timers === undefined) return;
    clearTimeout(timers.refresh);
    clearTimeout(timers.expire);
    this.tokenTimers.delete(connection);
  }
}

function redisExtension(config: Config): RedisExtension {
  const url = config.redis.url;
  const database =
    url.pathname === "" || url.pathname === "/"
      ? 0
      : Number(url.pathname.slice(1));
  if (!Number.isInteger(database) || database < 0)
    throw new Error("COLLABORATION_REDIS_URL database must be non-negative");
  return new RedisExtension({
    host: url.hostname,
    port: url.port === "" ? 6379 : Number(url.port),
    prefix: config.redis.prefix,
    identifier: config.instanceID,
    lockTimeout: config.redis.lockTimeoutMs,
    disconnectDelay: 250,
    awaitInitialSyncTimeout: 2_000,
    options: {
      db: database,
      connectTimeout: 5_000,
      enableReadyCheck: true,
      maxRetriesPerRequest: 1,
      ...(url.username === ""
        ? {}
        : { username: decodeURIComponent(url.username) }),
      ...(url.password === ""
        ? {}
        : { password: decodeURIComponent(url.password) }),
      ...(url.protocol === "rediss:"
        ? {
            tls: {
              servername: url.hostname,
              rejectUnauthorized: true,
              minVersion: "TLSv1.3",
            },
          }
        : {}),
    },
  });
}

function contextFromAuthorization(
  authorization: Authorization,
): CollaborationContext {
  return {
    access: authorization.access,
    permissionRevision: authorization.permissionRevision,
    ...(authorization.user === undefined
      ? {}
      : { user: copyUser(authorization.user) }),
    ...(authorization.tokenExpiresAt === undefined
      ? {}
      : { tokenExpiresAt: authorization.tokenExpiresAt }),
  };
}

function copyUser(user: PublicUser): PublicUser {
  return { id: user.id, username: user.username, avatar: user.avatar };
}

function normalizeToken(value: string): string | undefined {
  if (value === "") return undefined;
  if (value.trim() !== value || value.length > 16_384)
    throw new WebSocketProtocolError(4401, "unauthorized");
  return value;
}

function boundedTimerDelay(value: number): number {
  return Math.max(0, Math.min(value, 2_147_483_647));
}

function redisPublisher(value: unknown): { ping(): Promise<unknown> } {
  if (
    typeof value !== "object" ||
    value === null ||
    !("pub" in value) ||
    typeof value.pub !== "object" ||
    value.pub === null ||
    !("ping" in value.pub) ||
    typeof value.pub.ping !== "function"
  ) {
    throw new Error("Collaboration Redis publisher is not initialized");
  }
  return value.pub as { ping(): Promise<unknown> };
}

function authenticationProtocolError(error: unknown): WebSocketProtocolError {
  if (error instanceof WebSocketProtocolError) return error;
  if (error instanceof ServiceError) {
    if (error.status === 401)
      return new WebSocketProtocolError(4401, "unauthorized", { cause: error });
    if (error.status < 500)
      return new WebSocketProtocolError(4403, "forbidden", { cause: error });
  }
  return new WebSocketProtocolError(4503, "authorization-unavailable", {
    cause: error,
  });
}

class WebSocketProtocolError extends Error {
  constructor(
    readonly code: number,
    readonly reason: string,
    options?: ErrorOptions,
  ) {
    super(reason, options);
    this.name = "WebSocketProtocolError";
  }
}
