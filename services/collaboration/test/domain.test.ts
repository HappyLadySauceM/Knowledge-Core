import { v7 as uuidv7 } from "uuid";
import { describe, expect, it } from "vitest";

import {
  decodeVersionCursor,
  encodeVersionCursor,
  validateIdempotencyKey,
  validateLabel,
} from "../src/domain.js";

describe("version API domain boundaries", () => {
  it("round-trips opaque version cursors", () => {
    const cursor = {
      createdAt: new Date("2026-08-02T01:02:03.456Z"),
      id: uuidv7(),
    };
    expect(decodeVersionCursor(encodeVersionCursor(cursor))).toEqual(cursor);
  });

  it("rejects malformed cursors, labels, and idempotency keys", () => {
    expect(() => decodeVersionCursor("not-base64-json")).toThrow(
      "cursor is invalid",
    );
    expect(() => validateLabel("   ")).toThrow("label must contain");
    expect(() => validateIdempotencyKey(" leading-space")).toThrow(
      "Idempotency-Key",
    );
  });
});
