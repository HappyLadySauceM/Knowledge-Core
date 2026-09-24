import { Schema } from "prosemirror-model";
import { prosemirrorJSONToYXmlFragment } from "y-prosemirror";
import * as Y from "yjs";

export const FRAGMENT_NAME = "default";

const ATTACHMENT_ID = "01890f47-76a8-7b1c-b4db-1d9d3906f73b";
// Set before the first transaction so the committed update bytes are reproducible.
const FIXTURE_CLIENT_ID = 0x4b430001;
const STABLE_BLOCK_TYPES = new Set([
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
  "image",
  "attachment",
  "table",
  "tableRow",
  "tableHeader",
  "tableCell",
  "callout",
  "columns",
  "column",
  "formula",
]);

function blockAttrs(defaults = {}) {
  return { ...defaults, blockId: { default: null } };
}

export const richTextSchema = new Schema({
  nodes: {
    doc: { content: "block+" },
    paragraph: {
      content: "inline*",
      group: "block",
      attrs: blockAttrs({ textAlign: { default: "left" } }),
    },
    heading: {
      content: "inline*",
      group: "block",
      attrs: blockAttrs({ level: {}, textAlign: { default: "left" } }),
    },
    bulletList: { content: "listItem+", group: "block", attrs: blockAttrs() },
    orderedList: {
      content: "listItem+",
      group: "block",
      attrs: blockAttrs({ start: { default: 1 } }),
    },
    listItem: { content: "paragraph block*", attrs: blockAttrs() },
    taskList: { content: "taskItem+", group: "block", attrs: blockAttrs() },
    taskItem: {
      content: "paragraph block*",
      attrs: blockAttrs({ checked: { default: false } }),
    },
    blockquote: { content: "block+", group: "block", attrs: blockAttrs() },
    codeBlock: {
      content: "text*",
      marks: "",
      group: "block",
      code: true,
      attrs: blockAttrs({ language: { default: null } }),
    },
    horizontalRule: { group: "block", attrs: blockAttrs() },
    hardBreak: { inline: true, group: "inline", selectable: false },
    text: { group: "inline" },
    image: {
      group: "block",
      atom: true,
      attrs: blockAttrs({
        attachmentId: {},
        alt: { default: null },
        title: { default: null },
      }),
    },
    attachment: {
      group: "block",
      atom: true,
      attrs: blockAttrs({ attachmentId: {}, title: { default: null } }),
    },
    table: { content: "tableRow+", group: "block", attrs: blockAttrs() },
    tableRow: { content: "(tableHeader | tableCell)+", attrs: blockAttrs() },
    tableHeader: {
      content: "block+",
      attrs: blockAttrs({
        colspan: { default: 1 },
        rowspan: { default: 1 },
        colwidth: { default: null },
      }),
    },
    tableCell: {
      content: "block+",
      attrs: blockAttrs({
        colspan: { default: 1 },
        rowspan: { default: 1 },
        colwidth: { default: null },
      }),
    },
    callout: {
      content: "block+",
      group: "block",
      attrs: blockAttrs({ variant: { default: "info" } }),
    },
    columns: {
      content: "column{2,4}",
      group: "block",
      attrs: blockAttrs(),
    },
    column: { content: "block+", attrs: blockAttrs() },
    formula: {
      content: "text*",
      group: "block",
      code: true,
      attrs: blockAttrs(),
    },
  },
  marks: {
    bold: {},
    italic: {},
    strike: {},
    underline: {},
    code: { excludes: "_" },
    link: {
      attrs: { href: {}, title: { default: null } },
      inclusive: false,
    },
  },
});

const projectionTruthWithoutBlockIds = {
  type: "doc",
  content: [
    {
      type: "paragraph",
      attrs: { textAlign: "center" },
      content: [
        { type: "text", marks: [{ type: "bold" }], text: "Bold" },
        { type: "text", text: " " },
        { type: "text", marks: [{ type: "italic" }], text: "Italic" },
        { type: "text", text: " " },
        { type: "text", marks: [{ type: "strike" }], text: "Strike" },
        { type: "text", text: " " },
        {
          type: "text",
          marks: [{ type: "underline" }],
          text: "Underline",
        },
        { type: "text", text: " " },
        {
          type: "text",
          marks: [
            {
              type: "link",
              attrs: {
                href: "https://example.com/path",
                title: "Example link",
              },
            },
          ],
          text: "Link",
        },
        { type: "hardBreak" },
        { type: "text", marks: [{ type: "code" }], text: "Code" },
      ],
    },
    {
      type: "heading",
      attrs: { level: 2, textAlign: "right" },
      content: [{ type: "text", text: "Heading" }],
    },
    {
      type: "bulletList",
      content: [
        {
          type: "listItem",
          content: [
            {
              type: "paragraph",
              attrs: { textAlign: "left" },
              content: [{ type: "text", text: "Bullet" }],
            },
          ],
        },
      ],
    },
    {
      type: "orderedList",
      attrs: { start: 3 },
      content: [
        {
          type: "listItem",
          content: [
            {
              type: "paragraph",
              attrs: { textAlign: "left" },
              content: [{ type: "text", text: "Ordered" }],
            },
          ],
        },
      ],
    },
    {
      type: "taskList",
      content: [
        {
          type: "taskItem",
          attrs: { checked: false },
          content: [
            {
              type: "paragraph",
              attrs: { textAlign: "left" },
              content: [{ type: "text", text: "Open task" }],
            },
          ],
        },
        {
          type: "taskItem",
          attrs: { checked: true },
          content: [
            {
              type: "paragraph",
              attrs: { textAlign: "left" },
              content: [{ type: "text", text: "Done task" }],
            },
          ],
        },
      ],
    },
    {
      type: "blockquote",
      content: [
        {
          type: "paragraph",
          attrs: { textAlign: "left" },
          content: [{ type: "text", text: "Quote" }],
        },
      ],
    },
    {
      type: "codeBlock",
      attrs: { language: "rust" },
      content: [{ type: "text", text: "fn main() {}" }],
    },
    {
      type: "callout",
      attrs: { variant: "warning" },
      content: [
        {
          type: "paragraph",
          attrs: { textAlign: "left" },
          content: [{ type: "text", text: "Notice" }],
        },
      ],
    },
    {
      type: "columns",
      content: [
        {
          type: "column",
          content: [
            {
              type: "paragraph",
              attrs: { textAlign: "left" },
              content: [{ type: "text", text: "Left column" }],
            },
          ],
        },
        {
          type: "column",
          content: [
            {
              type: "paragraph",
              attrs: { textAlign: "left" },
              content: [{ type: "text", text: "Right column" }],
            },
          ],
        },
      ],
    },
    {
      type: "formula",
      content: [{ type: "text", text: "x^2 + y^2" }],
    },
    { type: "horizontalRule" },
    {
      type: "image",
      attrs: {
        attachmentId: ATTACHMENT_ID,
        alt: "architecture diagram",
        title: "diagram.png",
      },
    },
    {
      type: "attachment",
      attrs: { attachmentId: ATTACHMENT_ID, title: "notes.txt" },
    },
    {
      type: "table",
      content: [
        {
          type: "tableRow",
          content: [
            {
              type: "tableHeader",
              attrs: { colspan: 2, rowspan: 1, colwidth: [120, 120] },
              content: [
                {
                  type: "paragraph",
                  attrs: { textAlign: "left" },
                  content: [{ type: "text", text: "Header" }],
                },
              ],
            },
          ],
        },
        {
          type: "tableRow",
          content: [
            {
              type: "tableCell",
              attrs: { colspan: 1, rowspan: 1, colwidth: [120] },
              content: [
                {
                  type: "paragraph",
                  attrs: { textAlign: "left" },
                  content: [{ type: "text", text: "Left" }],
                },
              ],
            },
            {
              type: "tableCell",
              attrs: { colspan: 1, rowspan: 1, colwidth: [120] },
              content: [
                {
                  type: "paragraph",
                  attrs: { textAlign: "justify" },
                  content: [{ type: "text", text: "Right" }],
                },
              ],
            },
          ],
        },
      ],
    },
  ],
};

function addStableBlockIds(value) {
  let next = 1;
  const visit = (node) => {
    const result = {
      ...node,
      ...(node.content ? { content: node.content.map(visit) } : {}),
    };
    if (!STABLE_BLOCK_TYPES.has(node.type)) return result;
    return {
      ...result,
      attrs: {
        ...node.attrs,
        blockId: `fixture-block-${next++}`,
      },
    };
  };
  return visit(value);
}

export const projectionTruth = addStableBlockIds(
  projectionTruthWithoutBlockIds,
);

export const plainTextTruth = [
  "Bold Italic Strike Underline Link\nCode",
  "Heading",
  "Bullet",
  "Ordered",
  "Open task",
  "Done task",
  "Quote",
  "fn main() {}",
  "Notice",
  "Left column",
  "Right column",
  "x^2 + y^2",
  "Header",
  "Left",
  "Right",
].join("\n");

export function createYjsFixture() {
  const document = new Y.Doc();
  document.clientID = FIXTURE_CLIENT_ID;
  prosemirrorJSONToYXmlFragment(
    richTextSchema,
    projectionTruth,
    document.getXmlFragment(FRAGMENT_NAME),
  );

  return {
    format: "yjs-update-v1",
    generated_by: {
      yjs: "13.6.31",
      "y-prosemirror": "1.3.7",
      "prosemirror-model": "1.25.11",
    },
    state_base64: Buffer.from(Y.encodeStateAsUpdate(document)).toString(
      "base64",
    ),
    projection: projectionTruth,
    plain_text: plainTextTruth,
  };
}
