import { CollaborationServer } from "./collaboration/server.js";
import type { Config } from "./config.js";
import { loadConfig } from "./config.js";
import { InternalServer, type Readiness } from "./http/internal-server.js";
import { KnowledgeClient } from "./knowledge-client.js";
import { Logger } from "./logger.js";
import { NatsInvalidator } from "./nats-invalidator.js";
import { CollaborationStore } from "./storage/store.js";
import { VersionService } from "./version-service.js";
import { Workers } from "./workers.js";

export class Runtime implements Readiness {
  readonly config: Config;
  readonly logger: Logger;
  private store: CollaborationStore | undefined;
  private knowledge: KnowledgeClient | undefined;
  private collaboration: CollaborationServer | undefined;
  private invalidator: NatsInvalidator | undefined;
  private internal: InternalServer | undefined;
  private workers: Workers | undefined;
  private accepting = false;
  private live = true;
  private shutdownPromise: Promise<void> | undefined;

  private constructor(config: Config, logger: Logger) {
    this.config = config;
    this.logger = logger;
  }

  static async start(config: Config = loadConfig()): Promise<Runtime> {
    const runtime = new Runtime(config, new Logger());
    try {
      runtime.store = await CollaborationStore.open(config.postgres);
      runtime.knowledge = new KnowledgeClient(config.knowledge);
      runtime.invalidator = new NatsInvalidator(
        config.nats,
        (documentID) => runtime.collaboration?.closeDocument(documentID),
        runtime.logger,
      );
      await runtime.invalidator.start();
      runtime.collaboration = new CollaborationServer(
        config,
        runtime.store,
        runtime.knowledge,
        runtime.logger,
      );
      const versions = new VersionService(
        runtime.store,
        runtime.knowledge,
        runtime.invalidator,
      );
      runtime.internal = new InternalServer(
        config.internalServer,
        versions,
        runtime,
        runtime.logger,
      );
      runtime.workers = new Workers(
        config.workers,
        runtime.store,
        runtime.knowledge,
        runtime.logger,
      );

      await runtime.collaboration.listen();
      await runtime.internal.listen();
      runtime.workers.start();
      runtime.accepting = true;
      await runtime.ready();
      runtime.logger.info("Collaboration service started", {
        component: "collaboration.runtime",
        event: "started",
        environment: config.environment,
      });
      return runtime;
    } catch (error) {
      runtime.logger.error("Collaboration startup failed", error, {
        component: "collaboration.runtime",
      });
      try {
        await runtime.shutdown();
      } catch (shutdownError) {
        runtime.logger.error(
          "Collaboration startup cleanup failed",
          shutdownError,
          {
            component: "collaboration.runtime",
          },
        );
      }
      throw error;
    }
  }

  isLive(): boolean {
    return this.live;
  }

  async ready(): Promise<void> {
    if (!this.accepting)
      throw new Error("Collaboration service is not accepting traffic");
    const store = required(this.store, "PostgreSQL store");
    const knowledge = required(this.knowledge, "Knowledge client");
    const collaboration = required(this.collaboration, "WebSocket server");
    const invalidator = required(this.invalidator, "NATS invalidator");
    await Promise.all([
      store.ping(),
      knowledge.ping(),
      collaboration.ready(),
      invalidator.ping(),
    ]);
  }

  shutdown(): Promise<void> {
    this.shutdownPromise ??= this.runShutdown();
    return this.shutdownPromise;
  }

  private async runShutdown(): Promise<void> {
    this.accepting = false;
    const failures: unknown[] = [];
    await closeStep(this.internal, failures);
    await closeStep(this.collaboration, failures);
    await closeStep(this.workers, failures);
    await closeStep(this.invalidator, failures);
    this.knowledge?.close();
    await closeStep(this.store, failures);
    this.live = false;
    this.logger.info("Collaboration service stopped", {
      component: "collaboration.runtime",
      event: "stopped",
    });
    if (failures.length > 0)
      throw new AggregateError(failures, "Collaboration shutdown failed");
  }
}

interface Closeable {
  close(): void | Promise<void>;
}

async function closeStep(
  component: Closeable | undefined,
  failures: unknown[],
): Promise<void> {
  if (component === undefined) return;
  try {
    await component.close();
  } catch (error) {
    failures.push(error);
  }
}

function required<T>(value: T | undefined, description: string): T {
  if (value === undefined) throw new Error(`${description} is not initialized`);
  return value;
}
