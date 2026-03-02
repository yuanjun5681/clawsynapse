# NanoClaw Agno Agent Container
# Runs Agno-based agent in Docker container with Pilot Protocol support

# Stage 1: Build pilotctl from source
FROM golang:1.25-alpine AS pilot-build
RUN go install github.com/TeoSlayer/pilotprotocol/cmd/pilotctl@latest

# Stage 2: Agent runtime
FROM python:3.11-slim

# Install system dependencies
RUN apt-get update && apt-get install -y \
    curl \
    git \
    socat \
    && rm -rf /var/lib/apt/lists/*

# Copy pilotctl binary from build stage
COPY --from=pilot-build /go/bin/pilotctl /usr/local/bin/pilotctl

# Install uv for fast dependency management
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

# Create app directory
WORKDIR /app

# Copy dependency files first for layer caching
COPY agent-runner/pyproject.toml agent-runner/uv.lock agent-runner/.python-version ./

# Install dependencies using the system Python (avoid uv downloading to /root)
RUN uv sync --frozen --no-dev --python-preference only-system

# Copy source code (will be overwritten by host mount for hot-reload)
COPY agent-runner/src/ ./src/

# Create workspace directories (matching existing container layout)
RUN mkdir -p /workspace/group /workspace/global /workspace/extra \
    /workspace/ipc/messages /workspace/ipc/tasks /workspace/ipc/input

# Copy entrypoint
COPY agent-runner/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Create agent user (uid 1000) for compatibility with existing mount ownership
RUN groupadd -g 1000 agent && useradd -u 1000 -g agent -m agent

# Set ownership to agent user for writable directories
RUN chown -R agent:agent /workspace /app

# Switch to non-root user
USER agent

# Set working directory to group workspace
WORKDIR /workspace/group

# Entry point reads JSON from stdin, outputs JSON to stdout
ENTRYPOINT ["/app/entrypoint.sh"]
