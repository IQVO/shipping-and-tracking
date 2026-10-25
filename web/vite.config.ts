import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";

// shipping-and-tracking-mfe: the shipping-and-tracking remote. Exposes ./App
// -- the warehouse-console shell lazy-loads it as `shipping_mfe/App` and
// mounts it under its own `/shipping-and-tracking/*` splat route. Also
// runnable standalone on :4020 for local development without the shell (see
// main.tsx).
//
// PINNED host/remote contract (warehouse-console vite.config.ts will carry the
// matching entry; change both together or not at all):
//   federation container  shipping_mfe
//   exposed module        ./App (default export, no props)
//   production base       /mfes/shipping-and-tracking/   (Nginx web gateway path)
//   dev/preview port      4020 (off the console's 5181-5197 ladder, whose
//                          every slot an existing remote holds, and clear of
//                          the 4010-4016 range the read-only remotes took)
//
// `base` is the deployment-time asset namespace. In the kind cluster this
// remote is served by its own nginx pod behind the Nginx web gateway at
// http://localhost/mfes/shipping-and-tracking/, so every hashed chunk and the
// federation remoteEntry.js must resolve under that prefix -- otherwise a
// dynamically imported chunk would request /assets/... at the shell's origin
// root and collide with every other remote's assets.
//
// Kept as a plain object rather than the ({ command }) => ({...}) callback
// form on purpose: a callback export cannot be merged by Vite's mergeConfig
// ("Cannot merge config in form of callback"), which has killed whole test
// suites elsewhere in the fleet. Reading the command off process.argv keeps
// this a static object. (vitest.config.ts here is standalone and does not
// import this file at all.)
const IS_BUILD = process.argv.includes("build");
const PUBLIC_BASE = IS_BUILD ? "/mfes/shipping-and-tracking/" : "/";

export default defineConfig({
  base: PUBLIC_BASE,
  plugins: [
    react(),
    federation({
      name: "shipping_mfe",
      filename: "remoteEntry.js",
      // No federated type archive: the console imports this remote behind
      // `// @ts-expect-error` and never downloads its types, and the DTS
      // worker cannot compile through the solution-style tsconfig.json
      // (no `jsx` at the root), so it would only ever log a TYPE-001 error.
      dts: false,
      exposes: {
        "./App": "./src/App.tsx",
      },
      shared: {
        react: { singleton: true, requiredVersion: "^19.2.8" },
        "react-dom": { singleton: true, requiredVersion: "^19.2.8" },
        "react-router-dom": { singleton: true, requiredVersion: "^7.18.3" },
        "@warehouse/ui-kit": { singleton: true },
      },
    }),
  ],
  server: {
    port: 4020,
    strictPort: true,
    cors: true,
    origin: "http://localhost:4020",
  },
  preview: {
    port: 4020,
    strictPort: true,
    cors: true,
  },
  build: {
    target: "esnext",
    modulePreload: false,
  },
});
