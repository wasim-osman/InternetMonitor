// Copies the Linux agent binaries into src-tauri/resources/agents so they are bundled as resources.
// Source: $IM_AGENTS_DIR or ../agent/dist (repo). Missing source = keep whatever is already there
// (CI copies the `agents` artifact in directly before building).
import { existsSync, readdirSync, copyFileSync, chmodSync, mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const dest = join(here, "..", "src-tauri", "resources", "agents");
const src = process.env.IM_AGENTS_DIR || join(here, "..", "..", "agent", "dist");
mkdirSync(dest, { recursive: true });

// normalise names: imonitord-linux-armv7 → imonitord-linux-arm (what the installer looks up first)
const rename = (n) => n.replace(/-armv7$/, "-arm").replace(/-x86_64$/, "-amd64").replace(/-i386$/, "-386");

if (!existsSync(src)) {
  console.warn(`[agents] ${src} not found — keeping existing contents of ${dest}`);
} else {
  const files = readdirSync(src).filter((f) => /^imonitord-linux-[a-z0-9_]+$/.test(f));
  if (files.length === 0) console.warn(`[agents] no imonitord-linux-* binaries in ${src}`);
  for (const f of files) {
    const to = join(dest, rename(f));
    copyFileSync(join(src, f), to);
    try { chmodSync(to, 0o755); } catch {}
    console.log(`[agents] ${f} → resources/agents/${rename(f)}`);
  }
}
const have = readdirSync(dest).filter((f) => f.startsWith("imonitord-linux-"));
console.log(`[agents] bundled: ${have.join(", ") || "(none — 'Install agent via SSH' will be unavailable)"}`);
