import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { isAbsolute, relative, resolve } from "node:path";
import type { Plugin } from "vite";

const sha256 = (data: string | Uint8Array) => createHash("sha256").update(data).digest("hex");

export function bundleInventory(): Plugin {
  let root: string;

  function packagePath(id: string) {
    const modulePath = id.replace(/^\0/, "").split("?")[0];
    const packages = [...modulePath.matchAll(/\/node_modules\/((?:@[^/]+\/)?[^/]+)/g)];
    const last = packages.at(-1);
    if (!last) return undefined;
    const path = relative(root, modulePath.slice(0, last.index + last[0].length));
    if (isAbsolute(path) || path.startsWith("../") || !path.startsWith("node_modules/")) {
      throw new Error("Bundled npm package is outside the dashboard lockfile root");
    }
    return { path, modulePath };
  }

  return {
    name: "cairn-bundle-inventory",
    apply: "build",
    configResolved(config) {
      root = config.root;
    },
    generateBundle: {
      order: "post",
      handler(_options, bundle) {
        const lockBytes = readFileSync(resolve(root, "package-lock.json"));
        const lock = JSON.parse(lockBytes.toString());
        const chunks: Record<string, string[]> = {};
        const files: Record<string, string> = {};
        const packages: Record<string, { name: string; version: string }> = {};

        for (const id of this.getModuleIds()) {
          const entry = packagePath(id);
          if (entry && !/\.(?:[cm]?[jt]sx?|json)$/.test(entry.modulePath)) {
            throw new Error(`Bundled npm asset has no JavaScript module provenance: ${entry.path}`);
          }
        }

        for (const output of Object.values(bundle)) {
          if (output.type === "asset") {
            files[output.fileName] = sha256(output.source);
            continue;
          }
          const dependencies = new Set<string>();
          for (const id of output.moduleIds) {
            if (output.modules[id].renderedLength === 0) continue;
            const entry = packagePath(id);
            if (!entry) continue;
            const locked = lock.packages[entry.path];
            const installed = JSON.parse(readFileSync(resolve(root, entry.path, "package.json"), "utf-8"));
            const lockedName = locked?.name ?? entry.path.split("node_modules/").at(-1);
            if (installed.name !== lockedName || installed.version !== locked?.version) {
              throw new Error(`Bundled npm package disagrees with package-lock.json: ${entry.path}`);
            }
            packages[entry.path] = { name: installed.name, version: installed.version };
            dependencies.add(entry.path);
          }
          files[output.fileName] = sha256(output.code);
          chunks[output.fileName] = [...dependencies].sort((a, b) => a.localeCompare(b, "en"));
        }
        for (const output of Object.values(bundle)) {
          if (output.type === "chunk" &&
              [...output.imports, ...output.dynamicImports].some(name => !(name in chunks))) {
            throw new Error("Dashboard chunk imports JavaScript outside the bundle inventory");
          }
        }
        this.emitFile({
          type: "asset",
          fileName: ".cairn-bundle-inventory.json",
          source: JSON.stringify({ schema: 1, lock_sha256: sha256(lockBytes), files, chunks, packages }, null, 2) + "\n",
        });
      },
    },
  };
}
