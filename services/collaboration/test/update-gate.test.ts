import {
  Connection,
  Document,
  IncomingMessage,
  MessageReceiver,
  OutgoingMessage,
  type WebSocketLike,
} from "@hocuspocus/server";
import { describe, expect, it } from "vitest";
import * as Y from "yjs";

import {
  DurableUpdateGate,
  type CollaborationContext,
  type DurableUpdateStore,
} from "../src/collaboration/update-gate.js";
import { createInitialState, projectionFromDocument } from "../src/richtext.js";

const documentID = "0198a3c0-0000-7000-8000-000000000001";
const actor = { id: 42, username: "editor", avatar: "" };

class StoreStub implements DurableUpdateStore {
  calls = 0;
  observedPlainText = "";

  constructor(
    private readonly document: Document,
    private readonly failure?: Error,
  ) {}

  appendUpdate(): Promise<number> {
    this.calls += 1;
    this.observedPlainText = projectionFromDocument(this.document).plainText;
    return this.failure === undefined
      ? Promise.resolve(1)
      : Promise.reject(this.failure);
  }
}

describe("DurableUpdateGate", () => {
  it("does not apply or acknowledge an update when persistence fails", async () => {
    const document = serverDocument();
    const before = Y.encodeStateAsUpdate(document);
    const store = new StoreStub(document, new Error("database unavailable"));
    const gate = new DurableUpdateGate(store, 1 << 20, 16 << 20);
    const { receiver, connection, replies } = incomingUpdate(document, gate);

    await expect(receiver.apply(document, connection)).rejects.toThrow(
      "update-persistence-failed",
    );

    expect(store.calls).toBe(1);
    expect(store.observedPlainText).toBe("");
    expect(Y.encodeStateAsUpdate(document)).toEqual(before);
    expect(replies).toHaveLength(0);
    connection.close();
    document.destroy();
  });

  it("persists before Hocuspocus applies the update", async () => {
    const document = serverDocument();
    const store = new StoreStub(document);
    const gate = new DurableUpdateGate(store, 1 << 20, 16 << 20);
    const { receiver, connection } = incomingUpdate(document, gate);

    await receiver.apply(document, connection);

    expect(store.calls).toBe(1);
    expect(store.observedPlainText).toBe("");
    expect(projectionFromDocument(document).plainText).toBe("persist me");
    connection.close();
    document.destroy();
  });

  it("never persists updates from a read-only connection", async () => {
    const document = serverDocument();
    const store = new StoreStub(document);
    const gate = new DurableUpdateGate(store, 1 << 20, 16 << 20);
    const { receiver, connection } = incomingUpdate(document, gate, true);

    await receiver.apply(document, connection);

    expect(store.calls).toBe(0);
    expect(projectionFromDocument(document).plainText).toBe("");
    connection.close();
    document.destroy();
  });
});

function serverDocument(): Document {
  const document = new Document(documentID);
  Y.applyUpdate(document, createInitialState());
  return document;
}

function incomingUpdate(
  document: Document,
  gate: DurableUpdateGate,
  readOnly = false,
): {
  receiver: MessageReceiver;
  connection: Connection<CollaborationContext>;
  replies: Uint8Array[];
} {
  const client = new Y.Doc();
  Y.applyUpdate(client, Y.encodeStateAsUpdate(document));
  const paragraph = client.getXmlFragment("default").get(0);
  if (!(paragraph instanceof Y.XmlElement))
    throw new Error("initial paragraph is missing");
  paragraph.insert(0, [new Y.XmlText("persist me")]);
  const update = Y.encodeStateAsUpdate(client, Y.encodeStateVector(document));
  client.destroy();

  const replies: Uint8Array[] = [];
  const socket: WebSocketLike = {
    readyState: 1,
    send: (message) => {
      if (message instanceof Uint8Array) replies.push(message);
    },
    close: () => undefined,
  };
  const context: CollaborationContext = {
    access: readOnly ? "viewer" : "editor",
    permissionRevision: 1,
    user: actor,
  };
  const connection = new Connection(
    socket,
    new Request("http://localhost/collaboration"),
    document,
    "socket-1",
    context,
    readOnly,
  );
  connection.beforeSync((_connection, payload) =>
    gate.beforeSync({
      clientsCount: 1,
      context,
      document,
      documentName: documentID,
      connection,
      type: payload.type,
      payload: payload.payload,
    }),
  );
  const message = new OutgoingMessage(documentID)
    .createSyncMessage()
    .writeUpdate(update)
    .toUint8Array();
  const incoming = new IncomingMessage(message);
  incoming.readVarString();
  return { receiver: new MessageReceiver(incoming), connection, replies };
}
