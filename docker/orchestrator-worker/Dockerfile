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
      openssh-client \
      procps \
      jq && \
    npm install --global "pnpm@${PNPM_VERSION}" && \
    npm cache clean --force && \
    rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 10001 ao-worker && \
    useradd --uid 10001 --gid ao-worker --home-dir /workspace/.ao/home \
      --create-home --shell /bin/bash ao-worker && \
    mkdir -p /workspace /workspace/.ao/home /tmp/ao-worker && \
    chown -R ao-worker:ao-worker /workspace /tmp/ao-worker

ENV HOME=/workspace/.ao/home \
    GO111MODULE=on \
    GOTOOLCHAIN=auto \
    npm_config_update_notifier=false \
    npm_config_fund=false

USER ao-worker
WORKDIR /workspace

# The executor supplies the actual command through --entrypoint and argv.
# Keeping a harmless default makes the image safe to inspect interactively.
ENTRYPOINT ["/bin/true"]
