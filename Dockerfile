# Copyright (c) ZonaryOS. All rights reserved.
# Use of this source code is governed by the license found in the LICENSE
# file in the root of this repository (draft, pending legal review - see
# docs/OPEN_POINTS.md item 20).

# Backend image (see docs/OPEN_POINTS.md item 34): builds both the `server`
# and `migrate` binaries (cmd/server, cmd/migrate) into one image. Which one
# runs is chosen by the caller (docker-compose overrides `command:` for the
# migrate service) - both need the same source tree, so one build stage
# covers both rather than duplicating it across two Dockerfiles.
#
# No cloud-provider-specific base image or assumption here - plain
# golang/distroless, runs on any container host (Oracle, a VPS, a laptop).
# Explicitly ARM64-compatible: CGO is disabled (no libc dependency to cross-
# link) and GOARCH is taken from Docker's own TARGETARCH build arg, which
# BuildKit sets automatically for the requested --platform - this is what
# lets `docker buildx build --platform linux/arm64` (the Oracle Ampere A1
# target) produce a correct binary from the same Dockerfile used for local
# amd64 development, with no separate arm64-specific file.
# --platform=$BUILDPLATFORM pins this stage to the build machine's own
# architecture (not the target one) - Go's own cross-compiler emits
# GOARCH=arm64 machine code without ever having to execute arm64
# instructions during the build, so no QEMU/binfmt emulation is required
# for `docker buildx build --platform linux/arm64` on an amd64 build
# machine. Only the tiny final stage below actually needs the target
# platform's image, and it does nothing but copy files into it.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/migrate ./cmd/migrate

# distroless/static: no shell, no package manager, minimal attack surface -
# multi-arch (amd64/arm64/...), published by Google (not Oracle, AWS, or any
# other single cloud vendor), so this stays portable across deploy targets.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/migrate /usr/local/bin/migrate
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
