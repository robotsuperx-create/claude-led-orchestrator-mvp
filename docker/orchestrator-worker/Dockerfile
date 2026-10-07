# syntax=docker/dockerfile:1
# Dedicated runtime for the Claude-led orchestrator worker.
# It is intentionally separate from cloud/Dockerfile: the orchestrator worker
# executes fixed validation commands and needs both Go and Node toolchains.

ARG BASE_IMAGE=golang:1.27.1-bookworm
FROM ${BASE_IMAGE}

ARG PNPM_VERSION=10.12.4

USER root
RUN apt-get update && \
    apt-get install --yes --no-install-recommends \
      bash \
      ca-certificates \
      git \
      nodejs \
      npm \
      procps \
      jq && \
    npm install --global "pnpm@${PNPM_VERSION}" && \
    npm cache clean --force && \
    rm -rf /var/lib/apt/lists/*

# The image user is only a non-root fallback: on Linux the orchestrator runs
# the container as the host user so the bind-mounted worktree stays writable.
RUN groupadd --gid 10001 ao-worker && \
    useradd --uid 10001 --gid ao-worker --no-create-home --home-dir /tmp \
      --shell /usr/sbin/nologin ao-worker && \
    mkdir -p /workspace

# The container root filesystem is read-only and /workspace is the user's
# worktree, so every home directory and cache points at the /tmp tmpfs.
# GOTOOLCHAIN=local: the network is disabled, so Go must never try to download
# a different toolchain. Projects must vendor dependencies or use an image
# that pre-populates the module cache.
ENV HOME=/tmp \
    XDG_CACHE_HOME=/tmp/.cache \
    GOPATH=/tmp/go \
    GOCACHE=/tmp/.cache/go-build \
    GOTOOLCHAIN=local \
    GOFLAGS=-buildvcs=false \
    GO111MODULE=on \
    npm_config_cache=/tmp/.npm \
    npm_config_update_notifier=false \
    npm_config_fund=false

USER ao-worker
WORKDIR /workspace

# The executor supplies the actual command through --entrypoint and argv.
# Keeping a harmless default makes the image safe to inspect interactively.
ENTRYPOINT ["/bin/true"]
