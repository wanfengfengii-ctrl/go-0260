// Deterministic build: validate the console source files and produce the
// production dist directory that the Go binary embeds. The Go embed directive
// in webembed.go points at these same source files, so a rebuild is optional
// for the dev server but required for a reproducible artifact.
import { readFile, writeFile, mkdir, copyFile } from "node:fs/promises";
import { existsSync } from "node:fs";

const src = ["index.html", "app.js", "styles.css"];
for (const f of src) {
  if (!existsSync(f)) {
    throw new Error(`missing source file: ${f}`);
  }
}
await mkdir("dist", { recursive: true });
for (const f of src) {
  const data = await readFile(f, "utf8");
  await copyFile(f, `dist/${f}`);
}
console.log(`built ${src.length} frontend assets into dist/`);
