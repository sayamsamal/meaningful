import { defineConfig } from "vite";
import { nitro } from "nitro/vite";
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
    nitro(),
  ],
  // Nitro config lives here (read by the official nitro/vite plugin). The SolidStart
  // 2.0-alpha vite-plugin-nitro-2 produces broken Cloudflare output (solid-start#2115);
  // the official plugin + `exportConditions: ["worker"]` resolves worker-safe builds and
  // fixes the runtime "Illegal invocation" in sendWebResponse.
  nitro: {
    preset: "cloudflare_module",
    exportConditions: ["worker"],
  },
});
