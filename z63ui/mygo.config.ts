import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "Z63",
  identifier: "dev.qemu-z63.z63ui",
  version: "0.1.0",
  devUrl: "http://localhost:5173",
  devCommand: "bun run dev:web",
  buildCommand: "bun run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
});
