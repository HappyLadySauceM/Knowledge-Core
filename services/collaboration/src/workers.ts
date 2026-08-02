import type { Config } from "./config.js";
import type { KnowledgeClient } from "./knowledge-client.js";
import type { Logger } from "./logger.js";
import { projectionFromState } from "./richtext.js";
import type { CollaborationStore, ProjectionJob } from "./storage/store.js";

export class Workers {
  private readonly controller = new AbortController();
  private loop: Promise<void> | undefined;

  constructor(
    private readonly config: Config["workers"],
    private readonly store: CollaborationStore,
    private readonly knowledge: KnowledgeClient,
    private readonly logger: Logger,
  ) {}

  start(): void {
    if (this.loop !== undefined)
      throw new Error("Collaboration workers are already running");
    this.loop = this.run();
  }

  async close(): Promise<void> {
    this.controller.abort();
    await this.loop;
  }

  private async run(): Promise<void> {
    let cleanupCounter = 0;
    while (!this.controller.signal.aborted) {
      await this.projectOne();
      await this.runOperation("compaction", () =>
        this.store.compactNext(
          this.config.snapshotUpdateThreshold,
          this.config.snapshotByteThreshold,
        ),
      );
      await this.runOperation("automatic_version", () =>
        this.store.createAutomaticVersion(
          this.config.automaticVersionIntervalMs,
        ),
      );
      cleanupCounter += 1;
      if (cleanupCounter >= 300) {
        cleanupCounter = 0;
        await this.runOperation("idempotency_cleanup", async () => {
          await this.store.cleanupExpiredIdempotency();
          return true;
        });
      }
      await cancellableDelay(
        this.config.pollIntervalMs,
        this.controller.signal,
      );
    }
  }

  private async projectOne(): Promise<void> {
    let job: ProjectionJob | undefined;
    try {
      job = await this.store.claimProjectionJob(this.config.operationTimeoutMs);
      if (job === undefined) return;
      const projection = projectionFromState(job.state);
      await this.knowledge.project(
        job.documentID,
        job.sequence,
        projection.content,
        projection.plainText,
      );
      await this.store.completeProjection(job.documentID, job.sequence);
    } catch (error) {
      this.logger.error("Collaboration projection worker failed", error, {
        component: "collaboration.worker",
        operation: "projection",
      });
      if (job !== undefined) {
        const delay = Math.min(60_000, 500 * 2 ** Math.min(job.attempts, 7));
        try {
          await this.store.retryProjection(
            job.documentID,
            "projection_failed",
            delay,
          );
        } catch (retryError) {
          this.logger.error(
            "Collaboration projection retry scheduling failed",
            retryError,
            {
              component: "collaboration.worker",
              operation: "projection_retry",
            },
          );
        }
      }
    }
  }

  private async runOperation(
    name: string,
    operation: () => Promise<boolean>,
  ): Promise<void> {
    try {
      await operation();
    } catch (error) {
      this.logger.error("Collaboration background operation failed", error, {
        component: "collaboration.worker",
        operation: name,
      });
    }
  }
}

function cancellableDelay(
  milliseconds: number,
  signal: AbortSignal,
): Promise<void> {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, milliseconds);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(timer);
        resolve();
      },
      { once: true },
    );
  });
}
