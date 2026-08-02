import type { beforeSyncPayload } from "@hocuspocus/server";

import type { PublicUser } from "../domain.js";
import { validateActor } from "../domain.js";
import { ServiceError } from "../errors.js";
import { validateCandidateUpdate } from "../richtext.js";

export interface CollaborationContext {
  readonly access: "viewer" | "editor" | "owner";
  readonly permissionRevision: number;
  readonly user?: PublicUser;
  readonly tokenExpiresAt?: Date;
}

export interface DurableUpdateStore {
  appendUpdate(
    documentID: string,
    update: Uint8Array,
    actor: PublicUser,
  ): Promise<number>;
}

export class DurableUpdateGate {
  constructor(
    private readonly store: DurableUpdateStore,
    private readonly maxUpdateBytes: number,
    private readonly maxDocumentBytes: number,
  ) {}

  async beforeSync(
    payload: beforeSyncPayload<CollaborationContext>,
  ): Promise<void> {
    if (
      (payload.type !== 1 && payload.type !== 2) ||
      payload.connection.readOnly
    )
      return;
    try {
      const actor = validateActor(payload.context.user);
      if (
        !validateCandidateUpdate(
          payload.document,
          payload.payload,
          this.maxUpdateBytes,
          this.maxDocumentBytes,
        )
      )
        return;
      await this.store.appendUpdate(
        payload.documentName,
        payload.payload,
        actor,
      );
    } catch (error) {
      if (error instanceof ServiceError && error.status < 500) {
        throw new SyncProtocolError(4400, "invalid-update", { cause: error });
      }
      throw new SyncProtocolError(4503, "update-persistence-failed", {
        cause: error,
      });
    }
  }
}

class SyncProtocolError extends Error {
  constructor(
    readonly code: number,
    readonly reason: string,
    options?: ErrorOptions,
  ) {
    super(reason, options);
    this.name = "SyncProtocolError";
  }
}
