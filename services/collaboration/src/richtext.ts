import * as Y from "yjs";
import { yDocToProsemirrorJSON } from "y-prosemirror";

import { invalidInput } from "./errors.js";
import { isRecord, validateDocumentID } from "./domain.js";

const allowedNodes = new Set([
  "paragraph",
  "heading",
  "bulletList",
  "orderedList",
  "listItem",
  "taskList",
  "taskItem",
  "blockquote",
  "codeBlock",
  "horizontalRule",
  "hardBreak",
  "text",
  "image",
  "attachment",
  "table",
  "tableRow",
  "tableHeader",
  "tableCell",
]);
const allowedMarks = new Set([
  "bold",
  "italic",
  "strike",
  "underline",
  "code",
  "link",
]);
const allowedAttrs = new Set([
  "level",
  "start",
  "checked",
  "language",
  "href",
  "attachmentId",
  "alt",
  "title",
  "textAlign",
  "colspan",
  "rowspan",
  "colwidth",
]);
const blockNodes = new Set([
  "paragraph",
  "heading",
  "listItem",
  "taskItem",
  "blockquote",
  "codeBlock",
  "tableRow",
]);

export interface Projection {
  readonly content: Record<string, unknown>;
  readonly plainText: string;
}

export function createInitialState(): Uint8Array {
  const document = new Y.Doc();
  const fragment = document.getXmlFragment("default");
  fragment.insert(0, [new Y.XmlElement("paragraph")]);
  const state = Y.encodeStateAsUpdate(document);
  document.destroy();
  return state;
}

export function documentFromState(state: Uint8Array): Y.Doc {
  const document = new Y.Doc();
  try {
    Y.applyUpdate(document, state);
    projectionFromDocument(document);
    return document;
  } catch (error) {
    document.destroy();
    throw invalidInput("persisted collaborative state is invalid", {
      cause: error,
    });
  }
}

export function validateCandidateUpdate(
  document: Y.Doc,
  update: Uint8Array,
  maxUpdateBytes: number,
  maxDocumentBytes: number,
): boolean {
  if (update.byteLength === 0 || update.byteLength > maxUpdateBytes) {
    throw invalidInput(
      "collaboration update exceeds the configured size boundary",
    );
  }
  const currentState = Y.encodeStateAsUpdate(document);
  const candidate = new Y.Doc();
  try {
    Y.applyUpdate(candidate, currentState);
    const before = Y.encodeStateVector(candidate);
    Y.applyUpdate(candidate, update);
    const changed = !equalBytes(before, Y.encodeStateVector(candidate));
    if (!changed) return false;
    const state = Y.encodeStateAsUpdate(candidate);
    if (state.byteLength > maxDocumentBytes) {
      throw invalidInput(
        "collaborative document exceeds the configured size boundary",
      );
    }
    projectionFromDocument(candidate);
    return true;
  } catch (error) {
    if (error instanceof Error && error.name === "ServiceError") throw error;
    throw invalidInput("collaboration update is invalid", { cause: error });
  } finally {
    candidate.destroy();
  }
}

export function projectionFromState(state: Uint8Array): Projection {
  const document = documentFromState(state);
  try {
    return projectionFromDocument(document);
  } finally {
    document.destroy();
  }
}

export function projectionFromDocument(document: Y.Doc): Projection {
  // The schema-free serializer is required because the service accepts the
  // complete repository rich-text node set, not only StarterKit's schema.
  // eslint-disable-next-line @typescript-eslint/no-deprecated
  const value: unknown = yDocToProsemirrorJSON(document, "default");
  validateRichTextDocument(value);
  return { content: value, plainText: extractPlainText(value) };
}

export function restoreState(
  current: Y.Doc,
  targetState: Uint8Array,
): { update: Uint8Array; state: Uint8Array } {
  const target = documentFromState(targetState);
  try {
    const before = Y.encodeStateVector(current);
    const currentFragment = current.getXmlFragment("default");
    const targetFragment = target.getXmlFragment("default");
    current.transact(() => {
      if (currentFragment.length > 0)
        currentFragment.delete(0, currentFragment.length);
      const cloned = targetFragment.toArray().map((item) => {
        if (!(item instanceof Y.XmlElement) && !(item instanceof Y.XmlText)) {
          throw invalidInput(
            "version contains an unsupported collaborative XML node",
          );
        }
        return item.clone();
      });
      if (cloned.length > 0) currentFragment.insert(0, cloned);
    }, "version-restore");
    projectionFromDocument(current);
    return {
      update: Y.encodeStateAsUpdate(current, before),
      state: Y.encodeStateAsUpdate(current),
    };
  } finally {
    target.destroy();
  }
}

export function validateRichTextDocument(
  value: unknown,
): asserts value is Record<string, unknown> {
  if (
    !isRecord(value) ||
    value.type !== "doc" ||
    !Array.isArray(value.content)
  ) {
    throw invalidInput(
      "content must be a ProseMirror doc with a content array",
    );
  }
  const count = { value: 0 };
  for (const node of value.content) validateNode(node, 1, count);
}

function validateNode(
  value: unknown,
  depth: number,
  count: { value: number },
): void {
  if (
    !isRecord(value) ||
    depth > 64 ||
    typeof value.type !== "string" ||
    !allowedNodes.has(value.type)
  ) {
    throw invalidInput("content contains an unsupported or deeply nested node");
  }
  count.value += 1;
  if (count.value > 100_000)
    throw invalidInput("content contains too many nodes");
  const content = value.content;
  const marks = value.marks;
  const attrs = value.attrs;
  if (content !== undefined && !Array.isArray(content))
    throw invalidInput("node content must be an array");
  if (marks !== undefined && !Array.isArray(marks))
    throw invalidInput("node marks must be an array");
  if (value.type === "text") {
    if (
      typeof value.text !== "string" ||
      content !== undefined ||
      attrs !== undefined
    ) {
      throw invalidInput("content contains an invalid text node");
    }
  } else if (value.text !== undefined) {
    throw invalidInput("non-text nodes must not contain text");
  }
  if (attrs !== undefined) validateAttrs(value.type, attrs);
  for (const mark of marks ?? []) validateMark(mark);
  for (const child of content ?? []) validateNode(child, depth + 1, count);
}

function validateMark(value: unknown): void {
  if (
    !isRecord(value) ||
    typeof value.type !== "string" ||
    !allowedMarks.has(value.type)
  ) {
    throw invalidInput("content contains an unsupported mark");
  }
  if (value.attrs !== undefined) validateAttrs(value.type, value.attrs);
  if (value.type === "link") {
    const href = isRecord(value.attrs) ? value.attrs.href : undefined;
    if (typeof href !== "string" || !validLink(href))
      throw invalidInput("content contains an invalid link mark");
  }
}

function validateAttrs(nodeType: string, value: unknown): void {
  if (
    !isRecord(value) ||
    Object.keys(value).some((key) => !allowedAttrs.has(key))
  ) {
    throw invalidInput("content contains unsupported attributes");
  }
  if (
    value.level !== undefined &&
    (!Number.isInteger(value.level) ||
      Number(value.level) < 1 ||
      Number(value.level) > 6)
  ) {
    throw invalidInput("heading level must be between 1 and 6");
  }
  if (
    value.start !== undefined &&
    (!Number.isInteger(value.start) || Number(value.start) < 1)
  ) {
    throw invalidInput("ordered list start must be positive");
  }
  if (value.attachmentId !== undefined) {
    if (typeof value.attachmentId !== "string")
      throw invalidInput("attachmentId must be a UUIDv7");
    validateDocumentID(value.attachmentId);
  }
  if (
    (nodeType === "image" || nodeType === "attachment") &&
    typeof value.attachmentId !== "string"
  ) {
    throw invalidInput("attachment nodes require attachmentId");
  }
  if (
    value.textAlign !== undefined &&
    (typeof value.textAlign !== "string" ||
      !["left", "center", "right", "justify"].includes(value.textAlign))
  ) {
    throw invalidInput("content contains an invalid text alignment");
  }
  for (const key of ["colspan", "rowspan"] as const) {
    const attribute = value[key];
    if (
      attribute !== undefined &&
      (!Number.isInteger(attribute) ||
        Number(attribute) < 1 ||
        Number(attribute) > 100)
    ) {
      throw invalidInput(`table ${key} is out of range`);
    }
  }
  if (
    value.colwidth !== undefined &&
    (!Array.isArray(value.colwidth) ||
      value.colwidth.some(
        (width) => !Number.isInteger(width) || Number(width) <= 0,
      ))
  ) {
    throw invalidInput("table colwidth is invalid");
  }
}

function validLink(value: string): boolean {
  if (value.length > 2048 || /[\r\n]/.test(value)) return false;
  try {
    const parsed = new URL(value);
    return ["http:", "https:", "mailto:"].includes(parsed.protocol);
  } catch {
    return false;
  }
}

function extractPlainText(root: Record<string, unknown>): string {
  const lines: string[] = [];
  let current = "";
  const visit = (value: unknown): void => {
    if (!isRecord(value)) return;
    if (value.type === "text" && typeof value.text === "string")
      current += value.text;
    if (value.type === "hardBreak") current += "\n";
    if (Array.isArray(value.content))
      for (const child of value.content) visit(child);
    if (
      typeof value.type === "string" &&
      blockNodes.has(value.type) &&
      current.trim() !== ""
    ) {
      lines.push(current.trim());
      current = "";
    }
  };
  visit(root);
  if (current.trim() !== "") lines.push(current.trim());
  return lines.join("\n");
}

function equalBytes(left: Uint8Array, right: Uint8Array): boolean {
  if (left.byteLength !== right.byteLength) return false;
  return left.every((value, index) => value === right[index]);
}
