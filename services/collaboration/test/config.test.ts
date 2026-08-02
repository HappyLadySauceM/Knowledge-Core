import { describe, expect, it } from "vitest";

import { loadConfig } from "../src/config.js";

describe("Collaboration configuration", () => {
  it("loads bounded development defaults", () => {
    const config = loadConfig({ COLLABORATION_ENVIRONMENT: "test" });
    expect(config.publicServer.port).toBe(8091);
    expect(config.internalServer.port).toBe(8092);
    expect(config.publicServer.websocketURL.href).toBe(
      "ws://localhost:8091/collaboration",
    );
  });

  it("requires encrypted production edges and mTLS", () => {
    expect(() =>
      loadConfig({
        COLLABORATION_ENVIRONMENT: "production",
        COLLABORATION_PUBLIC_WEBSOCKET_URL:
          "ws://collaboration.example.test/collaboration",
      }),
    ).toThrow("must use wss");
  });

  it("rejects partial TLS and NATS credential settings", () => {
    expect(() =>
      loadConfig({ COLLABORATION_INTERNAL_TLS_ENABLED: "true" }),
    ).toThrow("requires a server certificate and key");
    expect(() =>
      loadConfig({
        COLLABORATION_INTERNAL_TLS_ENABLED: "true",
        COLLABORATION_INTERNAL_TLS_CERT_FILE: "client.crt",
      }),
    ).toThrow("must be set together");
    expect(() =>
      loadConfig({ COLLABORATION_NATS_USERNAME: "service" }),
    ).toThrow("must be set together");
  });
});
