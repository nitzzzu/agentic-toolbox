FROM node:22-bookworm

# System tools
RUN apt-get update && apt-get install -y \
    python3 python3-pip python3-venv \
    jq ripgrep curl git bash \
    && rm -rf /var/lib/apt/lists/*

# uv (fast Python package manager)
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.cargo/bin:/root/.local/bin:$PATH"

# Global npm tools
RUN npm install -g pnpm typescript tsx

WORKDIR /workspace
