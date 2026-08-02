import { describe, expect, it } from "vitest";
import * as Y from "yjs";

import {
  createInitialState,
  documentFromState,
  projectionFromDocument,
  restoreState,
  validateCandidateUpdate,
} from "../src/richtext.js";

describe("rich-text state", () => {
  it("creates the canonical empty ProseMirror document", () => {
    const document = documentFromState(createInitialState());
    expect(projectionFromDocument(document)).toEqual({
      content: { type: "doc", content: [{ type: "paragraph" }] },
      plainText: "",
    });
    document.destroy();
  });

  it("restores content by producing a forward Yjs update", () => {
    const initial = createInitialState();
    const current = documentFromState(initial);
    insertText(current, "current");
    const target = documentFromState(initial);
    insertText(target, "version");

    const restored = restoreState(current, Y.encodeStateAsUpdate(target));

    expect(restored.update.byteLength).toBeGreaterThan(2);
    expect(projectionFromDocument(current).plainText).toBe("version");
    const reloaded = documentFromState(restored.state);
    expect(projectionFromDocument(reloaded).plainText).toBe("version");
    current.destroy();
    target.destroy();
    reloaded.destroy();
  });

  it("rejects updates that exceed the configured boundary", () => {
    const document = documentFromState(createInitialState());
    expect(() =>
      validateCandidateUpdate(document, new Uint8Array(1025), 1024, 4096),
    ).toThrow("size boundary");
    document.destroy();
  });
});

function insertText(document: Y.Doc, text: string): void {
  const paragraph = document.getXmlFragment("default").get(0);
  if (!(paragraph instanceof Y.XmlElement))
    throw new Error("initial paragraph is missing");
  paragraph.insert(0, [new Y.XmlText(text)]);
}
