import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

/** @type {import('next').NextConfig} */
const nextConfig = {
  output: "standalone",
  reactStrictMode: true,
  // Pin the tracing root to this app so `standalone` output is correct even
  // though the repo contains multiple lockfiles (backend tooling, etc.).
  outputFileTracingRoot: __dirname,
};

export default nextConfig;
