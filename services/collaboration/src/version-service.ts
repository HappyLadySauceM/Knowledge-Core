import type {
  Authorization,
  PublicUser,
  Version,
  VersionCursor,
} from "./domain.js";
import { encodeVersionCursor, validateActor } from "./domain.js";
import { forbidden, unauthenticated } from "./errors.js";
import type { KnowledgeClient } from "./knowledge-client.js";
import { projectionFromState } from "./richtext.js";
import type {
  CollaborationStore,
  RestoreResult,
  VersionPage,
} from "./storage/store.js";

export interface DocumentInvalidator {
  invalidate(
    documentID: string,
    reason: "permissions" | "restore" | "purge",
  ): Promise<void>;
}

export class VersionService {
  constructor(
    private readonly store: CollaborationStore,
    private readonly knowledge: KnowledgeClient,
    private readonly invalidator: DocumentInvalidator,
  ) {}

  async list(
    documentID: string,
    token: string | undefined,
    cursor: VersionCursor | undefined,
    limit: number,
  ): Promise<{
    items: readonly VersionResponse[];
    nextCursor?: string;
    hasMore: boolean;
  }> {
    await this.authenticatedAuthorization(documentID, token, false);
    const page: VersionPage = await this.store.listVersions(
      documentID,
      cursor,
      limit,
    );
    const last = page.items.at(-1);
    return {
      items: page.items.map(versionResponse),
      hasMore: page.hasMore,
      ...(page.hasMore && last !== undefined
        ? {
            nextCursor: encodeVersionCursor({
              createdAt: last.createdAt,
              id: last.id,
            }),
          }
        : {}),
    };
  }

  async create(
    documentID: string,
    token: string | undefined,
    label: string | undefined,
    idempotencyKey: string | undefined,
  ): Promise<VersionResponse> {
    const authorization = await this.authenticatedAuthorization(
      documentID,
      token,
      true,
    );
    const version = await this.store.createManualVersion({
      documentID,
      actor: validateActor(authorization.user),
      ...(label === undefined ? {} : { label }),
      ...(idempotencyKey === undefined ? {} : { idempotencyKey }),
    });
    return versionResponse(version);
  }

  async get(
    documentID: string,
    versionID: string,
    token: string | undefined,
  ): Promise<VersionDetailResponse> {
    await this.authenticatedAuthorization(documentID, token, false);
    return versionDetailResponse(
      await this.store.getVersion(documentID, versionID),
    );
  }

  async restore(
    documentID: string,
    versionID: string,
    expectedSequence: number,
    token: string | undefined,
    idempotencyKey: string | undefined,
  ): Promise<VersionResponse> {
    const authorization = await this.authenticatedAuthorization(
      documentID,
      token,
      true,
    );
    const result: RestoreResult = await this.store.restoreVersion({
      documentID,
      versionID,
      expectedSequence,
      actor: validateActor(authorization.user),
      ...(idempotencyKey === undefined ? {} : { idempotencyKey }),
    });
    await this.invalidator.invalidate(documentID, "restore");
    return versionResponse(result.version);
  }

  async purge(documentID: string): Promise<void> {
    await this.invalidator.invalidate(documentID, "purge");
    await this.store.purgeDocument(documentID);
  }

  private async authenticatedAuthorization(
    documentID: string,
    token: string | undefined,
    write: boolean,
  ): Promise<Authorization> {
    if (token === undefined) throw unauthenticated();
    const authorization = await this.knowledge.authorize(documentID, token);
    validateActor(authorization.user);
    if (write && authorization.access === "viewer") throw forbidden();
    return authorization;
  }
}

export interface VersionResponse {
  readonly id: string;
  readonly document_id: string;
  readonly sequence: number;
  readonly kind: Version["kind"];
  readonly label?: string;
  readonly created_by: {
    readonly id: number;
    readonly username: string;
    readonly avatar: string;
  };
  readonly created_at: string;
}

export interface VersionDetailResponse {
  readonly version: VersionResponse;
  readonly content: Record<string, unknown>;
  readonly plain_text: string;
}

function versionResponse(version: Version): VersionResponse {
  return {
    id: version.id,
    document_id: version.documentID,
    sequence: version.sequence,
    kind: version.kind,
    ...(version.label === undefined ? {} : { label: version.label }),
    created_by: userResponse(version.createdBy),
    created_at: version.createdAt.toISOString(),
  };
}

function versionDetailResponse(version: Version): VersionDetailResponse {
  const projection = projectionFromState(version.state);
  return {
    version: versionResponse(version),
    content: projection.content,
    plain_text: projection.plainText,
  };
}

function userResponse(user: PublicUser): {
  id: number;
  username: string;
  avatar: string;
} {
  return { id: user.id, username: user.username, avatar: user.avatar };
}
