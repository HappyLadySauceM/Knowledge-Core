import { URL } from "node:url";

export type Environment = "development" | "production" | "test";

export interface TLSFiles {
  readonly enabled: boolean;
  readonly caFile?: string;
  readonly certFile?: string;
  readonly keyFile?: string;
}

export interface Config {
  readonly environment: Environment;
  readonly instanceID: string;
  readonly shutdownTimeoutMs: number;
  readonly publicServer: {
    readonly host: string;
    readonly port: number;
    readonly websocketURL: URL;
    readonly allowedOrigins: ReadonlySet<string>;
    readonly maxUpdateBytes: number;
    readonly maxDocumentBytes: number;
    readonly tokenRefreshLeadMs: number;
    readonly tokenGraceMs: number;
  };
  readonly internalServer: {
    readonly host: string;
    readonly port: number;
    readonly maxBodyBytes: number;
    readonly tls: TLSFiles;
  };
  readonly postgres: {
    readonly url: string;
    readonly maxConnections: number;
    readonly connectionTimeoutMs: number;
    readonly idleTimeoutMs: number;
    readonly tls: TLSFiles;
  };
  readonly redis: {
    readonly url: URL;
    readonly prefix: string;
    readonly lockTimeoutMs: number;
  };
  readonly nats: {
    readonly servers: readonly string[];
    readonly name: string;
    readonly connectTimeoutMs: number;
    readonly tls: TLSFiles;
    readonly token?: string;
    readonly username?: string;
    readonly password?: string;
  };
  readonly knowledge: {
    readonly baseURL: URL;
    readonly requestTimeoutMs: number;
    readonly tls: TLSFiles;
  };
  readonly workers: {
    readonly pollIntervalMs: number;
    readonly operationTimeoutMs: number;
    readonly snapshotUpdateThreshold: number;
    readonly snapshotByteThreshold: number;
    readonly automaticVersionIntervalMs: number;
  };
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const environment = oneOf(
    env.COLLABORATION_ENVIRONMENT ?? "development",
    "COLLABORATION_ENVIRONMENT",
    ["development", "production", "test"] as const,
  );
  const publicWebsocketURL = absoluteURL(
    env.COLLABORATION_PUBLIC_WEBSOCKET_URL ??
      "ws://localhost:8091/collaboration",
    "COLLABORATION_PUBLIC_WEBSOCKET_URL",
    ["ws:", "wss:"],
  );
  const knowledgeBaseURL = absoluteURL(
    env.COLLABORATION_KNOWLEDGE_BASE_URL ?? "http://127.0.0.1:8090",
    "COLLABORATION_KNOWLEDGE_BASE_URL",
    ["http:", "https:"],
    true,
  );
  const redisURL = absoluteURL(
    env.COLLABORATION_REDIS_URL ?? "redis://127.0.0.1:6379/1",
    "COLLABORATION_REDIS_URL",
    ["redis:", "rediss:"],
    false,
    true,
  );
  const internalTLS = tlsFiles(env, "COLLABORATION_INTERNAL_TLS");
  const postgresTLS = tlsFiles(env, "COLLABORATION_POSTGRES_TLS");
  const natsTLS = tlsFiles(env, "COLLABORATION_NATS_TLS");
  const knowledgeTLS = tlsFiles(env, "COLLABORATION_KNOWLEDGE_TLS");
  if (
    internalTLS.enabled &&
    (internalTLS.certFile === undefined || internalTLS.keyFile === undefined)
  ) {
    throw new Error(
      "Collaboration internal TLS requires a server certificate and key",
    );
  }
  const allowedOrigins = origins(
    env.COLLABORATION_ALLOWED_ORIGINS ?? "http://localhost:3000",
  );
  const natsServers = commaList(
    env.COLLABORATION_NATS_SERVERS ?? "nats://127.0.0.1:4222",
  );
  const natsToken = optional(env.COLLABORATION_NATS_TOKEN);
  const natsUsername = optional(env.COLLABORATION_NATS_USERNAME);
  const natsPassword = optional(env.COLLABORATION_NATS_PASSWORD);
  if (
    natsToken !== undefined &&
    (natsUsername !== undefined || natsPassword !== undefined)
  ) {
    throw new Error(
      "COLLABORATION_NATS_TOKEN cannot be combined with username/password authentication",
    );
  }
  if ((natsUsername === undefined) !== (natsPassword === undefined)) {
    throw new Error(
      "COLLABORATION_NATS_USERNAME and COLLABORATION_NATS_PASSWORD must be set together",
    );
  }
  for (const [index, server] of natsServers.entries()) {
    absoluteURL(server, `COLLABORATION_NATS_SERVERS[${String(index)}]`, [
      "nats:",
      "tls:",
    ]);
  }

  if ((knowledgeBaseURL.protocol === "https:") !== knowledgeTLS.enabled) {
    throw new Error(
      "COLLABORATION_KNOWLEDGE_TLS settings must match COLLABORATION_KNOWLEDGE_BASE_URL",
    );
  }
  if (environment === "production") {
    if (publicWebsocketURL.protocol !== "wss:") {
      throw new Error(
        "production COLLABORATION_PUBLIC_WEBSOCKET_URL must use wss",
      );
    }
    if (allowedOrigins.size === 0) {
      throw new Error(
        "production COLLABORATION_ALLOWED_ORIGINS must not be empty",
      );
    }
    if (!internalTLS.enabled || internalTLS.caFile === undefined) {
      throw new Error("production Collaboration internal HTTP requires mTLS");
    }
    if (!postgresTLS.enabled || postgresTLS.caFile === undefined) {
      throw new Error(
        "production Collaboration PostgreSQL requires verified TLS",
      );
    }
    if (redisURL.protocol !== "rediss:") {
      throw new Error("production COLLABORATION_REDIS_URL must use rediss");
    }
    if (
      !knowledgeTLS.enabled ||
      knowledgeTLS.caFile === undefined ||
      knowledgeTLS.certFile === undefined ||
      knowledgeTLS.keyFile === undefined
    ) {
      throw new Error(
        "production Collaboration to Knowledge traffic requires mTLS",
      );
    }
    if (
      !natsTLS.enabled ||
      natsServers.some((server) => !server.startsWith("tls://"))
    ) {
      throw new Error("production Collaboration NATS connections require TLS");
    }
  }

  return {
    environment,
    instanceID: required(
      env.COLLABORATION_INSTANCE_ID ?? `collaboration-${String(process.pid)}`,
      "COLLABORATION_INSTANCE_ID",
    ),
    shutdownTimeoutMs: integer(
      env.COLLABORATION_SHUTDOWN_TIMEOUT_MS,
      30_000,
      1_000,
      120_000,
    ),
    publicServer: {
      host: required(
        env.COLLABORATION_PUBLIC_HOST ?? "0.0.0.0",
        "COLLABORATION_PUBLIC_HOST",
      ),
      port: integer(env.COLLABORATION_PUBLIC_PORT, 8091, 1, 65_535),
      websocketURL: publicWebsocketURL,
      allowedOrigins,
      maxUpdateBytes: integer(
        env.COLLABORATION_MAX_UPDATE_BYTES,
        1 << 20,
        1_024,
        16 << 20,
      ),
      maxDocumentBytes: integer(
        env.COLLABORATION_MAX_DOCUMENT_BYTES,
        16 << 20,
        1_024,
        1 << 30,
      ),
      tokenRefreshLeadMs: integer(
        env.COLLABORATION_TOKEN_REFRESH_LEAD_MS,
        60_000,
        1_000,
        15 * 60_000,
      ),
      tokenGraceMs: integer(
        env.COLLABORATION_TOKEN_GRACE_MS,
        15_000,
        0,
        5 * 60_000,
      ),
    },
    internalServer: {
      host: required(
        env.COLLABORATION_INTERNAL_HOST ?? "127.0.0.1",
        "COLLABORATION_INTERNAL_HOST",
      ),
      port: integer(env.COLLABORATION_INTERNAL_PORT, 8092, 1, 65_535),
      maxBodyBytes: integer(
        env.COLLABORATION_INTERNAL_MAX_BODY_BYTES,
        1 << 20,
        1_024,
        16 << 20,
      ),
      tls: internalTLS,
    },
    postgres: {
      url: required(
        env.COLLABORATION_POSTGRES_URL ??
          "postgres://knowledge_core@127.0.0.1:5432/knowledge_core",
        "COLLABORATION_POSTGRES_URL",
      ),
      maxConnections: integer(
        env.COLLABORATION_POSTGRES_MAX_CONNECTIONS,
        20,
        1,
        200,
      ),
      connectionTimeoutMs: integer(
        env.COLLABORATION_POSTGRES_CONNECT_TIMEOUT_MS,
        5_000,
        100,
        60_000,
      ),
      idleTimeoutMs: integer(
        env.COLLABORATION_POSTGRES_IDLE_TIMEOUT_MS,
        30_000,
        1_000,
        10 * 60_000,
      ),
      tls: postgresTLS,
    },
    redis: {
      url: redisURL,
      prefix: required(
        env.COLLABORATION_REDIS_PREFIX ?? "knowledge-core:collaboration",
        "COLLABORATION_REDIS_PREFIX",
      ),
      lockTimeoutMs: integer(
        env.COLLABORATION_REDIS_LOCK_TIMEOUT_MS,
        5_000,
        100,
        60_000,
      ),
    },
    nats: {
      servers: natsServers,
      name: required(
        env.COLLABORATION_NATS_NAME ?? "knowledge-core.collaboration",
        "COLLABORATION_NATS_NAME",
      ),
      connectTimeoutMs: integer(
        env.COLLABORATION_NATS_CONNECT_TIMEOUT_MS,
        5_000,
        100,
        60_000,
      ),
      tls: natsTLS,
      ...(natsToken === undefined ? {} : { token: natsToken }),
      ...(natsUsername === undefined
        ? {}
        : {
            username: natsUsername,
            password: requiredOptional(
              natsPassword,
              "COLLABORATION_NATS_PASSWORD",
            ),
          }),
    },
    knowledge: {
      baseURL: knowledgeBaseURL,
      requestTimeoutMs: integer(
        env.COLLABORATION_KNOWLEDGE_TIMEOUT_MS,
        5_000,
        100,
        60_000,
      ),
      tls: knowledgeTLS,
    },
    workers: {
      pollIntervalMs: integer(
        env.COLLABORATION_WORKER_POLL_MS,
        1_000,
        50,
        60_000,
      ),
      operationTimeoutMs: integer(
        env.COLLABORATION_WORKER_TIMEOUT_MS,
        30_000,
        1_000,
        5 * 60_000,
      ),
      snapshotUpdateThreshold: integer(
        env.COLLABORATION_SNAPSHOT_UPDATE_THRESHOLD,
        500,
        1,
        100_000,
      ),
      snapshotByteThreshold: integer(
        env.COLLABORATION_SNAPSHOT_BYTE_THRESHOLD,
        8 << 20,
        1_024,
        1 << 30,
      ),
      automaticVersionIntervalMs: integer(
        env.COLLABORATION_AUTOMATIC_VERSION_INTERVAL_MS,
        30 * 60_000,
        60_000,
        30 * 24 * 60 * 60_000,
      ),
    },
  };
}

function tlsFiles(env: NodeJS.ProcessEnv, prefix: string): TLSFiles {
  const enabled = boolean(env[`${prefix}_ENABLED`], false, `${prefix}_ENABLED`);
  const caFile = optional(env[`${prefix}_CA_FILE`]);
  const certFile = optional(env[`${prefix}_CERT_FILE`]);
  const keyFile = optional(env[`${prefix}_KEY_FILE`]);
  if (
    !enabled &&
    (caFile !== undefined || certFile !== undefined || keyFile !== undefined)
  ) {
    throw new Error(`${prefix} files cannot be set while TLS is disabled`);
  }
  if (enabled && (certFile === undefined) !== (keyFile === undefined)) {
    throw new Error(
      `${prefix}_CERT_FILE and ${prefix}_KEY_FILE must be set together`,
    );
  }
  return compactTLS({ enabled, caFile, certFile, keyFile });
}

function compactTLS(value: {
  enabled: boolean;
  caFile: string | undefined;
  certFile: string | undefined;
  keyFile: string | undefined;
}): TLSFiles {
  return {
    enabled: value.enabled,
    ...(value.caFile === undefined ? {} : { caFile: value.caFile }),
    ...(value.certFile === undefined ? {} : { certFile: value.certFile }),
    ...(value.keyFile === undefined ? {} : { keyFile: value.keyFile }),
  };
}

function origins(raw: string): ReadonlySet<string> {
  const result = new Set<string>();
  for (const [index, value] of commaList(raw).entries()) {
    const parsed = absoluteURL(
      value,
      `COLLABORATION_ALLOWED_ORIGINS[${String(index)}]`,
      ["http:", "https:"],
      true,
    );
    result.add(parsed.origin);
  }
  return result;
}

function commaList(raw: string): string[] {
  if (raw.trim() === "") return [];
  const values = raw.split(",").map((value) => value.trim());
  if (values.some((value) => value === ""))
    throw new Error("comma-separated configuration contains an empty value");
  return values;
}

function absoluteURL(
  raw: string,
  name: string,
  protocols: readonly string[],
  originOnly = false,
  allowCredentials = false,
): URL {
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch (error) {
    throw new Error(`${name} must be an absolute URL`, { cause: error });
  }
  if (
    !protocols.includes(parsed.protocol) ||
    parsed.hostname === "" ||
    (!allowCredentials && (parsed.username !== "" || parsed.password !== ""))
  ) {
    throw new Error(`${name} has an unsupported URL or embedded credentials`);
  }
  if (
    originOnly &&
    (parsed.pathname !== "/" || parsed.search !== "" || parsed.hash !== "")
  ) {
    throw new Error(
      `${name} must be an origin without a path, query, or fragment`,
    );
  }
  return parsed;
}

function required(value: string, name: string): string {
  if (value.trim() === "") throw new Error(`${name} is required`);
  return value.trim();
}

function optional(value: string | undefined): string | undefined {
  if (value === undefined || value.trim() === "") return undefined;
  return value.trim();
}

function requiredOptional(value: string | undefined, name: string): string {
  if (value === undefined) throw new Error(`${name} is required`);
  return value;
}

function integer(
  raw: string | undefined,
  fallback: number,
  minimum: number,
  maximum: number,
): number {
  if (raw === undefined || raw.trim() === "") return fallback;
  if (!/^(?:0|[1-9][0-9]*)$/.test(raw))
    throw new Error(`invalid integer configuration value ${raw}`);
  const value = Number(raw);
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw new Error(
      `integer configuration value must be between ${String(minimum)} and ${String(maximum)}`,
    );
  }
  return value;
}

function boolean(
  raw: string | undefined,
  fallback: boolean,
  name: string,
): boolean {
  if (raw === undefined || raw.trim() === "") return fallback;
  if (raw === "true") return true;
  if (raw === "false") return false;
  throw new Error(`${name} must be true or false`);
}

function oneOf<const T extends readonly string[]>(
  raw: string,
  name: string,
  values: T,
): T[number] {
  if (!values.includes(raw))
    throw new Error(`${name} must be one of ${values.join(", ")}`);
  return raw;
}
