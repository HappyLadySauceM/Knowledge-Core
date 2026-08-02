import { validate as validateUUID, version as uuidVersion } from "uuid";

import { invalidInput } from "./errors.js";

export interface PublicUser {
  readonly id: number;
  readonly username: string;
  readonly avatar: string;
}

export type Access = "viewer" | "editor" | "owner";

export interface Authorization {
  readonly documentID: string;
  readonly access: Access;
  readonly user?: PublicUser;
  readonly tokenExpiresAt?: Date;
  readonly permissionRevision: number;
}

export interface Version {
  readonly id: string;
  readonly documentID: string;
  readonly sequence: number;
  readonly kind: "manual" | "automatic" | "restoration";
  readonly label?: string;
  readonly createdBy: PublicUser;
  readonly createdAt: Date;
  readonly state: Uint8Array;
}

export interface VersionCursor {
  readonly createdAt: Date;
  readonly id: string;
}

export function validateDocumentID(value: string): string {
  const normalized = value.trim();
  if (!validateUUID(normalized) || uuidVersion(normalized) !== 7) {
    throw invalidInput("document_id must be a UUIDv7");
  }
  return normalized;
}

export function validateVersionID(value: string): string {
  const normalized = value.trim();
  if (!validateUUID(normalized) || uuidVersion(normalized) !== 7) {
    throw invalidInput("version_id must be a UUIDv7");
  }
  return normalized;
}

export function validateActor(user: PublicUser | undefined): PublicUser {
  if (
    user === undefined ||
    !Number.isSafeInteger(user.id) ||
    user.id <= 0 ||
    user.username.trim() === "" ||
    user.username.length > 32 ||
    user.avatar.length > 4096
  ) {
    throw invalidInput("authenticated user is invalid");
  }
  return user;
}

export function validateLabel(value: unknown): string | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== "string") throw invalidInput("label must be a string");
  const normalized = value.trim();
  if (normalized.length < 1 || Array.from(normalized).length > 200) {
    throw invalidInput("label must contain between 1 and 200 characters");
  }
  return normalized;
}

export function validateIdempotencyKey(
  value: string | undefined,
): string | undefined {
  if (value === undefined) return undefined;
  if (
    value.length < 1 ||
    value.length > 128 ||
    value.trim() !== value ||
    !/^[\x21-\x7e]+$/.test(value)
  ) {
    throw invalidInput(
      "Idempotency-Key must contain 1-128 visible ASCII characters",
    );
  }
  return value;
}

export function encodeVersionCursor(cursor: VersionCursor): string {
  return Buffer.from(
    JSON.stringify({
      v: 1,
      time: cursor.createdAt.toISOString(),
      id: cursor.id,
    }),
  ).toString("base64url");
}

export function decodeVersionCursor(
  value: string | undefined,
): VersionCursor | undefined {
  if (value === undefined || value === "") return undefined;
  if (value.length > 1024) throw invalidInput("cursor is too long");
  try {
    const decoded: unknown = JSON.parse(
      Buffer.from(value, "base64url").toString("utf8"),
    );
    if (
      !isRecord(decoded) ||
      decoded.v !== 1 ||
      typeof decoded.time !== "string" ||
      typeof decoded.id !== "string"
    ) {
      throw new Error("invalid cursor payload");
    }
    const createdAt = new Date(decoded.time);
    if (
      !Number.isFinite(createdAt.getTime()) ||
      createdAt.toISOString() !== decoded.time
    ) {
      throw new Error("invalid cursor time");
    }
    return { createdAt, id: validateVersionID(decoded.id) };
  } catch (error) {
    throw invalidInput("cursor is invalid", { cause: error });
  }
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
