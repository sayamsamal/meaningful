import { defineConfig } from "vite";
import { nitroV2Plugin as nitro } from "@solidjs/vite-plugin-nitro-2";
import solidSvg from "vite-plugin-solid-svg";

import { solidStart } from "@solidjs/start/config";

export default defineConfig({
  plugins: [
    solidStart(),
    solidSvg({
      svgo: {
        enabled: true,
        svgoConfig: {
          plugins: [
            // Keep the viewBox so CSS can resize the icon...
            { name: "preset-default", params: { overrides: { removeViewBox: false } } },
            // ...and strip the fixed width/height so CSS fully controls the size.
            "removeDimensions",
          ],
        },
      },
    }),
    nitro({
      // Build for Cloudflare Workers (module worker, workerd runtime).
      preset: "cloudflare-module",
      rollupConfig: { external: ["node:async_hooks"] },
    }),
  ],
});
