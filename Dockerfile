# toolbox base image
# The foundation for all specialized tool images.
# Contains general-purpose tools every agent needs.
FROM ubuntu:24.04

# Prevent interactive prompts during apt installs.
ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8

# Core system tools.
RUN apt-get update && apt-get install -y --no-install-recommends \
    # Shell and text processing
    bash curl wget jq \
    # Search
    ripgrep fd-find \
    # Source control
    git \
    # Build essentials
    build-essential make \
    # Python
    python3 python3-pip python3-venv \
    # Node.js (LTS via apt)
    nodejs npm \
    # Media
    ffmpeg \
    # Network debugging
    netcat-openbsd dnsutils \
    # Editors (for scripts that call them)
    vim nano \
    # Process management
    procps \
    # Archive tools
    tar gzip unzip \
    && rm -rf /var/lib/apt/lists/*

# fd is installed as fdfind on Ubuntu — alias to fd.
RUN ln -sf /usr/bin/fdfind /usr/local/bin/fd

# Upgrade pip and install commonly used Python packages.
RUN pip3 install --no-cache-dir --break-system-packages \
    requests \
    httpx \
    pyyaml \
    python-dotenv \
    rich \
    click \
    pandas \
    numpy

# Node global tools.
RUN npm install -g \
    typescript \
    ts-node \
    tsx

# OCI labels for toolbox catalog auto-discovery.
LABEL toolbox.type="base"
LABEL toolbox.description="General purpose: python3, node, rg, jq, curl, git, ffmpeg and more"
LABEL toolbox.handles=""

WORKDIR /workspace
CMD ["sleep", "infinity"]
