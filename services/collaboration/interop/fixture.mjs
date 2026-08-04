import { Schema } from "prosemirror-model";
import { prosemirrorJSONToYXmlFragment } from "y-prosemirror";
import * as Y from "yjs";

export const FRAGMENT_NAME = "default";

const ATTACHMENT_ID = "01890f47-76a8-7b1c-b4db-1d9d3906f73b";
// Set before the first transaction so the committed update bytes are reproducible.
const FIXTURE_CLIENT_ID = 0x4b430001;

export const richTextSchema = new Schema({
  nodes: {
    doc: { content: "block+" },
    paragraph: {
      content: "inline*",
      group: "block",
      attrs: { textAlign: { default: "left" } },
    },
    heading: {
      content: "inline*",
      group: "block",
      attrs: { level: {}, textAlign: { default: "left" } },
    },
    bulletList: { content: "listItem+", group: "block" },
    orderedList: {
      content: "listItem+",
      group: "block",
      attrs: { start: { default: 1 } },
    },
    listItem: { content: "paragraph block*" },
    taskList: { content: "taskItem+", group: "block" },
    taskItem: {
      content: "paragraph block*",
      attrs: { checked: { default: false } },
    },
    blockquote: { content: "block+", group: "block" },
    codeBlock: {
      content: "text*",
      marks: "",
      group: "block",
      code: true,
      attrs: { language: { default: null } },
    },
    horizontalRule: { group: "block" },
    hardBreak: { inline: true, group: "inline", selectable: false },
    text: { group: "inline" },
    image: {
      group: "block",
      atom: true,
      attrs: {
        attachmentId: {},
        alt: { default: null },
        title: { default: null },
      },
    },
    attachment: {
      group: "block",
      atom: true,
      attrs: { attachmentId: {}, title: { default: null } },
    },
    table: { content: "tableRow+", group: "block" },
    tableRow: { content: "(tableHeader | tableCell)+" },
    tableHeader: {
      content: "block+",
      attrs: {
        colspan: { default: 1 },
        rowspan: { default: 1 },
        colwidth: { default: null },
      },
    },
    tableCell: {
      content: "block+",
      attrs: {
        colspan: { default: 1 },
        rowspan: { default: 1 },
        colwidth: { default: null },
      },
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

export const projectionTruth = {
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

export const plainTextTruth = [
  "Bold Italic Strike Underline Link\nCode",
  "Heading",
  "Bullet",
  "Ordered",
  "Open task",
  "Done task",
  "Quote",
  "fn main() {}",
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
