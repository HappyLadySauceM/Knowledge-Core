import { writeFile } from "node:fs/promises";

import { format } from "prettier";

import { createYjsFixture } from "./fixture.mjs";

const output = new URL("./fixtures/yjs-update-v1.json", import.meta.url);
const contents = await format(JSON.stringify(createYjsFixture()), {
  parser: "json",
});
await writeFile(output, contents, "utf8");
