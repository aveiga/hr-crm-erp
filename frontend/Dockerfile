# Build context is the frontend directory.
#   docker build -f frontend/Dockerfile -t hrcrm-frontend ./frontend
#
# ---- deps ----
FROM node:22-bookworm-slim AS deps
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci

# ---- build ----
FROM node:22-bookworm-slim AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

# ---- runtime (standalone) ----
FROM node:22-bookworm-slim AS runtime
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
RUN useradd --create-home --uid 10001 appuser

COPY --from=build /app/public ./public
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static

USER appuser
EXPOSE 3000
# API_URL is read at runtime and injected into the browser (see app/layout.tsx),
# so this image is portable — set API_URL per deployment.
ENV PORT=3000 HOSTNAME=0.0.0.0 API_URL=http://localhost:8080
CMD ["node", "server.js"]
