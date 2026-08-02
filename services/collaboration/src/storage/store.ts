import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";

import type { PoolClient, QueryResultRow } from "pg";
import { Pool } from "pg";
import { v7 as uuidv7 } from "uuid";
import * as Y from "yjs";

import type { Config } from "../config.js";
import type { PublicUser, Version, VersionCursor } from "../domain.js";
import { conflict, internal, notFound, preconditionFailed } from "../errors.js";
import {
  createInitialState,
  documentFromState,
  restoreState,
} from "../richtext.js";
import { initialMigration } from "./migrations.js";

const migrationLockKey = 0x4b43434f4c4c4142n;
const idempotencyTTLMilliseconds = 24 * 60 * 60_000;

interface HeadRow extends QueryResultRow {
  document_id: string;
  current_sequence: string | number;
  last_snapshot_sequence: string | number;
  last_version_sequence: string | number;
  last_automatic_version_at: Date | string | null;
  last_actor_id: string | number | null;
  last_actor_username: string | null;
  last_actor_avatar: string | null;
  updated_at: Date | string;
}

interface StateRow extends QueryResultRow {
  sequence: string | number;
  state: Buffer;
}

interface UpdateRow extends QueryResultRow {
  sequence: string | number;
  update: Buffer;
}

interface VersionRow extends QueryResultRow {
  id: string;
  document_id: string;
  sequence: string | number;
  kind: "manual" | "automatic" | "restoration";
  label: string | null;
  state: Buffer;
  created_by_id: string | number;
  created_by_username: string;
  created_by_avatar: string;
  created_at: Date | string;
}

interface IdempotencyRow extends QueryResultRow {
  resource_id: string;
  request_hash: string;
}

export interface VersionPage {
  readonly items: readonly Version[];
  readonly hasMore: boolean;
}

export interface ProjectionJob {
  readonly documentID: string;
  readonly sequence: number;
  readonly state: Uint8Array;
  readonly attempts: number;
}

export interface RestoreResult {
  readonly version: Version;
  readonly changed: boolean;
}

export class CollaborationStore {
  constructor(private readonly pool: Pool) {}

  static async open(config: Config["postgres"]): Promise<CollaborationStore> {
    const pool = new Pool({
      connectionString: config.url,
      max: config.maxConnections,
      connectionTimeoutMillis: config.connectionTimeoutMs,
      idleTimeoutMillis: config.idleTimeoutMs,
      statement_timeout: Math.max(config.connectionTimeoutMs * 6, 30_000),
      query_timeout: Math.max(config.connectionTimeoutMs * 6, 30_000),
      allowExitOnIdle: false,
      ...(config.tls.enabled
        ? {
            ssl: {
              rejectUnauthorized: true,
              ...(config.tls.caFile === undefined
                ? {}
                : { ca: readFileSync(config.tls.caFile, "utf8") }),
              ...(config.tls.certFile === undefined
                ? {}
                : { cert: readFileSync(config.tls.certFile, "utf8") }),
              ...(config.tls.keyFile === undefined
                ? {}
                : { key: readFileSync(config.tls.keyFile, "utf8") }),
            },
          }
        : {}),
    });
    const store = new CollaborationStore(pool);
    try {
      await store.migrate();
      await store.ping();
      return store;
    } catch (error) {
      await pool.end();
      throw new Error("open Collaboration PostgreSQL store", { cause: error });
    }
  }

  async close(): Promise<void> {
    await this.pool.end();
  }

  async ping(): Promise<void> {
    await this.pool.query("SELECT 1");
  }

  async migrate(): Promise<void> {
    const client = await this.pool.connect();
    try {
      await client.query("BEGIN");
      await client.query("SELECT pg_advisory_xact_lock($1)", [
        migrationLockKey.toString(),
      ]);
      await client.query(`CREATE SCHEMA IF NOT EXISTS collaboration;
CREATE TABLE IF NOT EXISTS collaboration.schema_migrations (
  version bigint PRIMARY KEY,
  name text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
)`);
      const applied = await client.query<{ exists: boolean }>(
        "SELECT EXISTS (SELECT 1 FROM collaboration.schema_migrations WHERE version = 1) AS exists",
      );
      if (applied.rows[0]?.exists !== true) {
        await client.query(initialMigration);
        await client.query(
          "INSERT INTO collaboration.schema_migrations(version, name) VALUES (1, '001_initial')",
        );
      }
      await client.query("COMMIT");
    } catch (error) {
      await rollback(client);
      throw new Error("apply Collaboration database migrations", {
        cause: error,
      });
    } finally {
      client.release();
    }
  }

  async initializeDocument(documentID: string): Promise<void> {
    const initialState = createInitialState();
    await this.transaction(async (client) => {
      const now = new Date();
      const inserted = await client.query(
        `INSERT INTO collaboration.document_heads(
           document_id, current_sequence, last_snapshot_sequence, last_version_sequence, created_at, updated_at
         ) VALUES ($1, 0, 0, 0, $2, $2)
         ON CONFLICT (document_id) DO NOTHING`,
        [documentID, now],
      );
      if ((inserted.rowCount ?? 0) > 0) {
        await client.query(
          `INSERT INTO collaboration.document_snapshots(document_id, sequence, state, created_at)
           VALUES ($1, 0, $2, $3)`,
          [documentID, Buffer.from(initialState), now],
        );
      }
    });
  }

  async loadDocument(
    documentID: string,
  ): Promise<{ state: Uint8Array; sequence: number }> {
    const client = await this.pool.connect();
    try {
      const head = await this.loadHead(client, documentID, false);
      const sequence = numberFromDatabase(
        head.current_sequence,
        "current_sequence",
      );
      return {
        state: await this.loadState(client, documentID, sequence),
        sequence,
      };
    } finally {
      client.release();
    }
  }

  async appendUpdate(
    documentID: string,
    update: Uint8Array,
    actor: PublicUser,
  ): Promise<number> {
    return this.transaction(async (client) => {
      const head = await this.loadHead(client, documentID, true);
      const sequence =
        numberFromDatabase(head.current_sequence, "current_sequence") + 1;
      const now = new Date();
      await client.query(
        `INSERT INTO collaboration.document_updates(document_id, sequence, update, actor_id, created_at)
         VALUES ($1, $2, $3, $4, $5)`,
        [documentID, sequence, Buffer.from(update), actor.id, now],
      );
      await client.query(
        `UPDATE collaboration.document_heads
         SET current_sequence = $2, last_actor_id = $3, last_actor_username = $4,
             last_actor_avatar = $5, updated_at = $6
         WHERE document_id = $1`,
        [documentID, sequence, actor.id, actor.username, actor.avatar, now],
      );
      await enqueueProjection(client, documentID, sequence, now);
      return sequence;
    });
  }

  async currentSequence(documentID: string): Promise<number> {
    const result = await this.pool.query<Pick<HeadRow, "current_sequence">>(
      "SELECT current_sequence FROM collaboration.document_heads WHERE document_id = $1",
      [documentID],
    );
    const row = result.rows[0];
    if (row === undefined) throw notFound();
    return numberFromDatabase(row.current_sequence, "current_sequence");
  }

  async createManualVersion(input: {
    documentID: string;
    actor: PublicUser;
    label?: string;
    idempotencyKey?: string;
  }): Promise<Version> {
    const operation = `create_version:${input.documentID}`;
    const requestHash = hashRequest({
      documentID: input.documentID,
      label: input.label ?? null,
    });
    return this.transaction(async (client) => {
      const existing = await this.idempotentVersion(
        client,
        input.actor.id,
        operation,
        input.idempotencyKey,
        requestHash,
      );
      if (existing !== undefined) return existing;
      const head = await this.loadHead(client, input.documentID, true);
      const sequence = numberFromDatabase(
        head.current_sequence,
        "current_sequence",
      );
      const state = await this.loadState(client, input.documentID, sequence);
      const version = await this.insertVersion(client, {
        id: uuidv7(),
        documentID: input.documentID,
        sequence,
        kind: "manual",
        ...(input.label === undefined ? {} : { label: input.label }),
        state,
        actor: input.actor,
      });
      await client.query(
        `UPDATE collaboration.document_heads
         SET last_version_sequence = GREATEST(last_version_sequence, $2), updated_at = GREATEST(updated_at, $3)
         WHERE document_id = $1`,
        [input.documentID, sequence, version.createdAt],
      );
      await this.saveIdempotency(
        client,
        input.actor.id,
        operation,
        input.idempotencyKey,
        requestHash,
        version.id,
      );
      return version;
    });
  }

  async listVersions(
    documentID: string,
    cursor: VersionCursor | undefined,
    limit: number,
  ): Promise<VersionPage> {
    const values: unknown[] = [documentID];
    let cursorSQL = "";
    if (cursor !== undefined) {
      values.push(cursor.createdAt, cursor.id);
      cursorSQL = "AND (created_at, id) < ($2, $3::uuid)";
    }
    values.push(limit + 1);
    const limitParameter = values.length;
    const query = `SELECT id, document_id, sequence, kind, label, state,
              created_by_id, created_by_username, created_by_avatar, created_at
       FROM collaboration.document_versions
       WHERE document_id = $1 ${cursorSQL}
       ORDER BY created_at DESC, id DESC
       LIMIT $${String(limitParameter)}`;
    const result = await this.pool.query<VersionRow>(query, values);
    const hasMore = result.rows.length > limit;
    return { items: result.rows.slice(0, limit).map(versionFromRow), hasMore };
  }

  async getVersion(documentID: string, versionID: string): Promise<Version> {
    const result = await this.pool.query<VersionRow>(
      `SELECT id, document_id, sequence, kind, label, state,
              created_by_id, created_by_username, created_by_avatar, created_at
       FROM collaboration.document_versions WHERE document_id = $1 AND id = $2`,
      [documentID, versionID],
    );
    const row = result.rows[0];
    if (row === undefined) throw notFound();
    return versionFromRow(row);
  }

  async restoreVersion(input: {
    documentID: string;
    versionID: string;
    expectedSequence: number;
    actor: PublicUser;
    idempotencyKey?: string;
  }): Promise<RestoreResult> {
    const operation = `restore_version:${input.documentID}`;
    const requestHash = hashRequest({
      documentID: input.documentID,
      versionID: input.versionID,
      expectedSequence: input.expectedSequence,
    });
    return this.transaction(async (client) => {
      const existing = await this.idempotentVersion(
        client,
        input.actor.id,
        operation,
        input.idempotencyKey,
        requestHash,
      );
      if (existing !== undefined) return { version: existing, changed: false };
      const head = await this.loadHead(client, input.documentID, true);
      const currentSequence = numberFromDatabase(
        head.current_sequence,
        "current_sequence",
      );
      if (currentSequence !== input.expectedSequence)
        throw preconditionFailed();
      const target = await this.getVersionWithClient(
        client,
        input.documentID,
        input.versionID,
      );
      const currentState = await this.loadState(
        client,
        input.documentID,
        currentSequence,
      );
      const current = documentFromState(currentState);
      let restored: ReturnType<typeof restoreState>;
      try {
        restored = restoreState(current, target.state);
      } finally {
        current.destroy();
      }
      const changed = !isEmptyUpdate(restored.update);
      const sequence = changed ? currentSequence + 1 : currentSequence;
      const now = new Date();
      if (changed) {
        await client.query(
          `INSERT INTO collaboration.document_updates(document_id, sequence, update, actor_id, created_at)
           VALUES ($1, $2, $3, $4, $5)`,
          [
            input.documentID,
            sequence,
            Buffer.from(restored.update),
            input.actor.id,
            now,
          ],
        );
        await enqueueProjection(client, input.documentID, sequence, now);
      }
      const version = await this.insertVersion(client, {
        id: uuidv7(),
        documentID: input.documentID,
        sequence,
        kind: "restoration",
        label: `Restored from ${input.versionID}`,
        state: restored.state,
        actor: input.actor,
        now,
      });
      await client.query(
        `UPDATE collaboration.document_heads
         SET current_sequence = $2, last_version_sequence = $2,
             last_actor_id = $3, last_actor_username = $4, last_actor_avatar = $5, updated_at = $6
         WHERE document_id = $1`,
        [
          input.documentID,
          sequence,
          input.actor.id,
          input.actor.username,
          input.actor.avatar,
          now,
        ],
      );
      await this.saveIdempotency(
        client,
        input.actor.id,
        operation,
        input.idempotencyKey,
        requestHash,
        version.id,
      );
      return { version, changed };
    });
  }

  async createAutomaticVersion(intervalMs: number): Promise<boolean> {
    const candidate = await this.pool.query<Pick<HeadRow, "document_id">>(
      `SELECT document_id FROM collaboration.document_heads
       WHERE current_sequence > last_version_sequence
         AND last_actor_id IS NOT NULL
         AND (last_automatic_version_at IS NULL OR last_automatic_version_at <= now() - ($1 * interval '1 millisecond'))
       ORDER BY COALESCE(last_automatic_version_at, created_at), document_id
       LIMIT 1`,
      [intervalMs],
    );
    const documentID = candidate.rows[0]?.document_id;
    if (documentID === undefined) return false;
    return this.transaction(async (client) => {
      const head = await this.loadHead(client, documentID, true);
      const sequence = numberFromDatabase(
        head.current_sequence,
        "current_sequence",
      );
      const lastVersion = numberFromDatabase(
        head.last_version_sequence,
        "last_version_sequence",
      );
      const lastAutomatic = nullableDate(head.last_automatic_version_at);
      if (
        sequence <= lastVersion ||
        head.last_actor_id === null ||
        head.last_actor_username === null ||
        head.last_actor_avatar === null ||
        (lastAutomatic !== undefined &&
          Date.now() - lastAutomatic.getTime() < intervalMs)
      ) {
        return false;
      }
      const actor: PublicUser = {
        id: numberFromDatabase(head.last_actor_id, "last_actor_id"),
        username: head.last_actor_username,
        avatar: head.last_actor_avatar,
      };
      const state = await this.loadState(client, documentID, sequence);
      const now = new Date();
      await this.insertVersion(client, {
        id: uuidv7(),
        documentID,
        sequence,
        kind: "automatic",
        state,
        actor,
        now,
      });
      await client.query(
        `UPDATE collaboration.document_heads
         SET last_version_sequence = $2, last_automatic_version_at = $3
         WHERE document_id = $1`,
        [documentID, sequence, now],
      );
      return true;
    });
  }

  async claimProjectionJob(
    leaseMs: number,
  ): Promise<ProjectionJob | undefined> {
    return this.transaction(async (client) => {
      const result = await client.query<{
        document_id: string;
        attempts: number;
      }>(
        `SELECT document_id, attempts FROM collaboration.projection_jobs
         WHERE next_attempt_at <= now()
         ORDER BY next_attempt_at, updated_at
         FOR UPDATE SKIP LOCKED LIMIT 1`,
      );
      const row = result.rows[0];
      if (row === undefined) return undefined;
      const head = await this.loadHead(client, row.document_id, true);
      const sequence = numberFromDatabase(
        head.current_sequence,
        "current_sequence",
      );
      const attempts = row.attempts + 1;
      await client.query(
        `UPDATE collaboration.projection_jobs
         SET target_sequence = GREATEST(target_sequence, $2), attempts = $3,
             next_attempt_at = now() + ($4 * interval '1 millisecond'), updated_at = now()
         WHERE document_id = $1`,
        [row.document_id, sequence, attempts, leaseMs],
      );
      return {
        documentID: row.document_id,
        sequence,
        state: await this.loadState(client, row.document_id, sequence),
        attempts,
      };
    });
  }

  async completeProjection(
    documentID: string,
    sequence: number,
  ): Promise<void> {
    await this.transaction(async (client) => {
      const result = await client.query<{ target_sequence: string | number }>(
        "SELECT target_sequence FROM collaboration.projection_jobs WHERE document_id = $1 FOR UPDATE",
        [documentID],
      );
      const row = result.rows[0];
      if (row === undefined) return;
      if (
        numberFromDatabase(row.target_sequence, "target_sequence") <= sequence
      ) {
        await client.query(
          "DELETE FROM collaboration.projection_jobs WHERE document_id = $1",
          [documentID],
        );
      } else {
        await client.query(
          `UPDATE collaboration.projection_jobs
           SET attempts = 0, next_attempt_at = now(), last_error_key = '', updated_at = now()
           WHERE document_id = $1`,
          [documentID],
        );
      }
    });
  }

  async retryProjection(
    documentID: string,
    errorKey: string,
    delayMs: number,
  ): Promise<void> {
    await this.pool.query(
      `UPDATE collaboration.projection_jobs
       SET next_attempt_at = now() + ($2 * interval '1 millisecond'), last_error_key = $3, updated_at = now()
       WHERE document_id = $1`,
      [documentID, delayMs, errorKey.slice(0, 64)],
    );
  }

  async compactNext(
    updateThreshold: number,
    byteThreshold: number,
  ): Promise<boolean> {
    const candidate = await this.pool.query<{ document_id: string }>(
      `SELECT h.document_id
       FROM collaboration.document_heads h
       WHERE EXISTS (
         SELECT 1 FROM collaboration.document_updates u
         WHERE u.document_id = h.document_id AND u.sequence > h.last_snapshot_sequence
         GROUP BY u.document_id
         HAVING count(*) >= $1 OR COALESCE(sum(octet_length(u.update)), 0) >= $2
       )
       ORDER BY h.updated_at, h.document_id LIMIT 1`,
      [updateThreshold, byteThreshold],
    );
    const documentID = candidate.rows[0]?.document_id;
    if (documentID === undefined) return false;
    return this.transaction(async (client) => {
      const head = await this.loadHead(client, documentID, true);
      const sequence = numberFromDatabase(
        head.current_sequence,
        "current_sequence",
      );
      const lastSnapshot = numberFromDatabase(
        head.last_snapshot_sequence,
        "last_snapshot_sequence",
      );
      const size = await client.query<{
        update_count: string | number;
        update_bytes: string | number;
      }>(
        `SELECT count(*) AS update_count, COALESCE(sum(octet_length(update)), 0) AS update_bytes
         FROM collaboration.document_updates
         WHERE document_id = $1 AND sequence > $2 AND sequence <= $3`,
        [documentID, lastSnapshot, sequence],
      );
      const stats = size.rows[0];
      if (
        stats === undefined ||
        (numberFromDatabase(stats.update_count, "update_count") <
          updateThreshold &&
          numberFromDatabase(stats.update_bytes, "update_bytes") <
            byteThreshold)
      ) {
        return false;
      }
      const state = await this.loadState(client, documentID, sequence);
      await client.query(
        `INSERT INTO collaboration.document_snapshots(document_id, sequence, state, created_at)
         VALUES ($1, $2, $3, now()) ON CONFLICT (document_id, sequence) DO NOTHING`,
        [documentID, sequence, Buffer.from(state)],
      );
      await client.query(
        "UPDATE collaboration.document_heads SET last_snapshot_sequence = $2 WHERE document_id = $1",
        [documentID, sequence],
      );
      await client.query(
        "DELETE FROM collaboration.document_updates WHERE document_id = $1 AND sequence <= $2",
        [documentID, sequence],
      );
      await client.query(
        "DELETE FROM collaboration.document_snapshots WHERE document_id = $1 AND sequence < $2",
        [documentID, sequence],
      );
      return true;
    });
  }

  async purgeDocument(documentID: string): Promise<void> {
    await this.pool.query(
      "DELETE FROM collaboration.document_heads WHERE document_id = $1",
      [documentID],
    );
  }

  async cleanupExpiredIdempotency(): Promise<void> {
    await this.pool.query(
      "DELETE FROM collaboration.idempotency_keys WHERE expires_at < now()",
    );
  }

  private async transaction<T>(
    operation: (client: PoolClient) => Promise<T>,
  ): Promise<T> {
    const client = await this.pool.connect();
    try {
      await client.query("BEGIN");
      const result = await operation(client);
      await client.query("COMMIT");
      return result;
    } catch (error) {
      await rollback(client);
      throw error;
    } finally {
      client.release();
    }
  }

  private async loadHead(
    client: PoolClient,
    documentID: string,
    lock: boolean,
  ): Promise<HeadRow> {
    const result = await client.query<HeadRow>(
      `SELECT document_id, current_sequence, last_snapshot_sequence, last_version_sequence,
              last_automatic_version_at, last_actor_id, last_actor_username, last_actor_avatar, updated_at
       FROM collaboration.document_heads WHERE document_id = $1${lock ? " FOR UPDATE" : ""}`,
      [documentID],
    );
    const row = result.rows[0];
    if (row === undefined) throw notFound();
    return row;
  }

  private async loadState(
    client: PoolClient,
    documentID: string,
    targetSequence: number,
  ): Promise<Uint8Array> {
    const snapshots = await client.query<StateRow>(
      `SELECT sequence, state FROM collaboration.document_snapshots
       WHERE document_id = $1 AND sequence <= $2
       ORDER BY sequence DESC LIMIT 1`,
      [documentID, targetSequence],
    );
    const snapshot = snapshots.rows[0];
    if (snapshot === undefined)
      throw internal({ cause: new Error("document snapshot is missing") });
    const snapshotSequence = numberFromDatabase(
      snapshot.sequence,
      "snapshot_sequence",
    );
    const updates = await client.query<UpdateRow>(
      `SELECT sequence, update FROM collaboration.document_updates
       WHERE document_id = $1 AND sequence > $2 AND sequence <= $3
       ORDER BY sequence`,
      [documentID, snapshotSequence, targetSequence],
    );
    const document = documentFromState(snapshot.state);
    try {
      for (const row of updates.rows) Y.applyUpdate(document, row.update);
      return Y.encodeStateAsUpdate(document);
    } catch (error) {
      throw internal({ cause: error });
    } finally {
      document.destroy();
    }
  }

  private async insertVersion(
    client: PoolClient,
    input: {
      id: string;
      documentID: string;
      sequence: number;
      kind: Version["kind"];
      label?: string;
      state: Uint8Array;
      actor: PublicUser;
      now?: Date;
    },
  ): Promise<Version> {
    const now = input.now ?? new Date();
    const result = await client.query<VersionRow>(
      `INSERT INTO collaboration.document_versions(
         id, document_id, sequence, kind, label, state,
         created_by_id, created_by_username, created_by_avatar, created_at
       ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
       RETURNING id, document_id, sequence, kind, label, state,
                 created_by_id, created_by_username, created_by_avatar, created_at`,
      [
        input.id,
        input.documentID,
        input.sequence,
        input.kind,
        input.label ?? null,
        Buffer.from(input.state),
        input.actor.id,
        input.actor.username,
        input.actor.avatar,
        now,
      ],
    );
    const row = result.rows[0];
    if (row === undefined)
      throw internal({ cause: new Error("inserted version was not returned") });
    return versionFromRow(row);
  }

  private async getVersionWithClient(
    client: PoolClient,
    documentID: string,
    versionID: string,
  ): Promise<Version> {
    const result = await client.query<VersionRow>(
      `SELECT id, document_id, sequence, kind, label, state,
              created_by_id, created_by_username, created_by_avatar, created_at
       FROM collaboration.document_versions WHERE document_id = $1 AND id = $2`,
      [documentID, versionID],
    );
    const row = result.rows[0];
    if (row === undefined) throw notFound();
    return versionFromRow(row);
  }

  private async idempotentVersion(
    client: PoolClient,
    actorID: number,
    operation: string,
    key: string | undefined,
    requestHash: string,
  ): Promise<Version | undefined> {
    if (key === undefined) return undefined;
    await client.query(
      "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
      [`${String(actorID)}:${operation}:${key}`],
    );
    const result = await client.query<IdempotencyRow>(
      `SELECT resource_id, request_hash FROM collaboration.idempotency_keys
       WHERE actor_id = $1 AND operation = $2 AND key = $3 AND expires_at > now()
       FOR UPDATE`,
      [actorID, operation, key],
    );
    const row = result.rows[0];
    if (row === undefined) return undefined;
    if (row.request_hash !== requestHash)
      throw conflict("Idempotency-Key was already used with different input");
    const version = await client.query<VersionRow>(
      `SELECT id, document_id, sequence, kind, label, state,
              created_by_id, created_by_username, created_by_avatar, created_at
       FROM collaboration.document_versions WHERE id = $1`,
      [row.resource_id],
    );
    const versionRow = version.rows[0];
    if (versionRow === undefined)
      throw internal({ cause: new Error("idempotent version is missing") });
    return versionFromRow(versionRow);
  }

  private async saveIdempotency(
    client: PoolClient,
    actorID: number,
    operation: string,
    key: string | undefined,
    requestHash: string,
    resourceID: string,
  ): Promise<void> {
    if (key === undefined) return;
    const now = new Date();
    try {
      await client.query(
        `INSERT INTO collaboration.idempotency_keys(
           actor_id, operation, key, request_hash, resource_id, expires_at, created_at
         ) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
        [
          actorID,
          operation,
          key,
          requestHash,
          resourceID,
          new Date(now.getTime() + idempotencyTTLMilliseconds),
          now,
        ],
      );
    } catch (error) {
      if (postgresCode(error) === "23505")
        throw conflict("Idempotency-Key is already in use", { cause: error });
      throw error;
    }
  }
}

async function enqueueProjection(
  client: PoolClient,
  documentID: string,
  sequence: number,
  now: Date,
): Promise<void> {
  await client.query(
    `INSERT INTO collaboration.projection_jobs(
       document_id, target_sequence, attempts, next_attempt_at, created_at, updated_at
     ) VALUES ($1, $2, 0, $3, $3, $3)
     ON CONFLICT (document_id) DO UPDATE
     SET target_sequence = GREATEST(collaboration.projection_jobs.target_sequence, EXCLUDED.target_sequence),
         next_attempt_at = LEAST(collaboration.projection_jobs.next_attempt_at, EXCLUDED.next_attempt_at),
         updated_at = EXCLUDED.updated_at`,
    [documentID, sequence, now],
  );
}

function versionFromRow(row: VersionRow): Version {
  const label = row.label === null ? {} : { label: row.label };
  return {
    id: row.id,
    documentID: row.document_id,
    sequence: numberFromDatabase(row.sequence, "version.sequence"),
    kind: row.kind,
    ...label,
    state: new Uint8Array(row.state),
    createdBy: {
      id: numberFromDatabase(row.created_by_id, "version.created_by_id"),
      username: row.created_by_username,
      avatar: row.created_by_avatar,
    },
    createdAt: dateFromDatabase(row.created_at, "version.created_at"),
  };
}

function hashRequest(value: unknown): string {
  return createHash("sha256").update(JSON.stringify(value)).digest("hex");
}

function numberFromDatabase(value: string | number, field: string): number {
  const result = typeof value === "number" ? value : Number(value);
  if (!Number.isSafeInteger(result) || result < 0)
    throw internal({
      cause: new Error(`${field} is outside the safe integer range`),
    });
  return result;
}

function dateFromDatabase(value: Date | string, field: string): Date {
  const result = value instanceof Date ? value : new Date(value);
  if (!Number.isFinite(result.getTime()))
    throw internal({ cause: new Error(`${field} is invalid`) });
  return result;
}

function nullableDate(value: Date | string | null): Date | undefined {
  return value === null ? undefined : dateFromDatabase(value, "timestamp");
}

function postgresCode(error: unknown): string | undefined {
  if (typeof error !== "object" || error === null || !("code" in error))
    return undefined;
  return typeof error.code === "string" ? error.code : undefined;
}

function isEmptyUpdate(update: Uint8Array): boolean {
  return update.byteLength === 2 && update[0] === 0 && update[1] === 0;
}

async function rollback(client: PoolClient): Promise<void> {
  try {
    await client.query("ROLLBACK");
  } catch {
    // Preserve the original transaction error.
  }
}
