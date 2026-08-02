import { readFileSync } from "node:fs";

import { connect } from "@nats-io/transport-node";

import type { Config } from "./config.js";
import { isRecord, validateDocumentID } from "./domain.js";
import type { DocumentInvalidator } from "./version-service.js";
import type { Logger } from "./logger.js";

const permissionSubject = "knowledge.permissions.changed";
const invalidationSubject = "collaboration.documents.invalidated";

type NatsConnection = Awaited<ReturnType<typeof connect>>;
type NatsSubscription = ReturnType<NatsConnection["subscribe"]>;

export class NatsInvalidator implements DocumentInvalidator {
  private connection: NatsConnection | undefined;
  private subscriptions: NatsSubscription[] = [];
  private loops: Promise<void>[] = [];

  constructor(
    private readonly config: Config["nats"],
    private readonly closeDocument: (documentID: string) => void,
    private readonly logger: Logger,
  ) {}

  async start(): Promise<void> {
    this.connection = await connect({
      servers: [...this.config.servers],
      name: this.config.name,
      timeout: this.config.connectTimeoutMs,
      maxReconnectAttempts: -1,
      reconnectTimeWait: 2_000,
      noEcho: true,
      ...(this.config.token === undefined ? {} : { token: this.config.token }),
      ...(this.config.username === undefined
        ? {}
        : {
            user: this.config.username,
            pass: requiredCredential(this.config.password),
          }),
      ...(this.config.tls.enabled
        ? {
            tls: {
              ...(this.config.tls.caFile === undefined
                ? {}
                : { ca: readFileSync(this.config.tls.caFile, "utf8") }),
              ...(this.config.tls.certFile === undefined
                ? {}
                : { cert: readFileSync(this.config.tls.certFile, "utf8") }),
              ...(this.config.tls.keyFile === undefined
                ? {}
                : { key: readFileSync(this.config.tls.keyFile, "utf8") }),
            },
          }
        : { tls: null }),
    });
    for (const subject of [permissionSubject, invalidationSubject]) {
      const subscription = this.connection.subscribe(subject);
      this.subscriptions.push(subscription);
      this.loops.push(this.consume(subject, subscription));
    }
  }

  async invalidate(
    documentID: string,
    reason: "permissions" | "restore" | "purge",
  ): Promise<void> {
    const connection = this.requireConnection();
    connection.publish(
      invalidationSubject,
      new TextEncoder().encode(
        JSON.stringify({ document_id: documentID, reason }),
      ),
    );
    await connection.flush();
    this.closeDocument(documentID);
  }

  async ping(): Promise<void> {
    await this.requireConnection().flush();
  }

  async close(): Promise<void> {
    for (const subscription of this.subscriptions) subscription.unsubscribe();
    await Promise.allSettled(this.loops);
    this.subscriptions = [];
    this.loops = [];
    const connection = this.connection;
    this.connection = undefined;
    if (connection !== undefined && !connection.isClosed()) {
      let timer: NodeJS.Timeout | undefined;
      try {
        await Promise.race([
          connection.drain(),
          new Promise<never>((_resolve, reject) => {
            timer = setTimeout(
              () => reject(new Error("NATS drain timed out")),
              this.config.connectTimeoutMs,
            );
          }),
        ]);
      } catch {
        await connection.close();
      } finally {
        if (timer !== undefined) clearTimeout(timer);
      }
    }
  }

  private async consume(
    subject: string,
    subscription: NatsSubscription,
  ): Promise<void> {
    try {
      for await (const message of subscription) {
        try {
          const value: unknown = JSON.parse(
            new TextDecoder().decode(message.data),
          );
          if (!isRecord(value) || typeof value.document_id !== "string")
            continue;
          this.closeDocument(validateDocumentID(value.document_id));
        } catch (error) {
          this.logger.warn("ignored invalid Collaboration invalidation event", {
            component: "collaboration.nats",
            subject,
            error_type: error instanceof Error ? error.name : typeof error,
          });
        }
      }
    } catch (error) {
      if (this.connection !== undefined)
        this.logger.error("Collaboration NATS subscription stopped", error, {
          subject,
        });
    }
  }

  private requireConnection(): NatsConnection {
    if (this.connection === undefined || this.connection.isClosed())
      throw new Error("NATS connection is not available");
    return this.connection;
  }
}

function requiredCredential(value: string | undefined): string {
  if (value === undefined) throw new Error("NATS password is not configured");
  return value;
}
