# Copyright (c) ZonaryOS. All rights reserved.
# Use of this source code is governed by the license found in the LICENSE
# file in the root of this repository (draft, pending legal review - see
# docs/OPEN_POINTS.md item 20).

# Frontend image (see docs/OPEN_POINTS.md item 34). Multi-stage so the final
# image only carries Next.js's "standalone" output (see next.config.ts) - a
# pruned server bundle, not the full node_modules tree the build needs.
#
# node:22-alpine is a standard, multi-arch (amd64/arm64/...) official image
# - no cloud-provider-specific base or assumption. Cross-arch correctness
# for the Ampere A1 (arm64) target comes from using multi-arch base images
# throughout and building via `docker buildx build --platform linux/arm64`
# (BuildKit resolves each stage's image for that target platform on its
# own - no build-time GOARCH-style arg is needed here the way the Go backend
# needed one, since npm/Next produce arch-independent JS, and the only
# arch-specific piece, the node runtime itself, is already handled by
# BuildKit selecting the right node:22-alpine manifest).
FROM node:22-alpine AS deps
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci

FROM node:22-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY web/ .
RUN npm run build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
RUN addgroup -S zonaryos && adduser -S zonaryos -G zonaryos
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
USER zonaryos
EXPOSE 3000
ENV PORT=3000
ENV HOSTNAME=0.0.0.0
CMD ["node", "server.js"]
