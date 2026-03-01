# ==============================================================================
# TOOLS BUILDER STAGE: Download all pre-compiled binaries
# ==============================================================================
FROM python:3.14-slim AS tools-builder

# Tool versions (easily updatable) - verified latest as of 2026-02-22
ARG USQL_VERSION=0.20.8
ARG RAR_VERSION=624
ARG DUCKDB_VERSION=1.4.4
ARG MILLER_VERSION=6.17.0
ARG DASEL_VERSION=3.2.2
ARG RGA_VERSION=0.10.10
ARG HURL_VERSION=7.1.0
ARG COMBY_VERSION=1.8.1
ARG RIPGREP_VERSION=15.1.0
ARG FD_VERSION=10.3.0
ARG SD_VERSION=1.0.0
ARG GREX_VERSION=1.4.6
ARG OUCH_VERSION=0.6.1
ARG WATCHEXEC_VERSION=2.4.0
ARG SKIM_VERSION=3.5.0
ARG AST_GREP_VERSION=0.40.5
ARG DELTA_VERSION=0.18.2

# Install download tools
RUN apt-get update && \
    apt-get install -y \
    curl unzip bzip2 wget xz-utils \
    ca-certificates && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Download and prepare all tool binaries
WORKDIR /tmp
RUN mkdir -p /tools-binaries

# usql
RUN curl -fsSL https://github.com/xo/usql/releases/download/v${USQL_VERSION}/usql-${USQL_VERSION}-linux-amd64.tar.bz2 | tar xjf - -C /tools-binaries

# rar/unrar
RUN wget -q https://www.rarlab.com/rar/rarlinux-x64-${RAR_VERSION}.tar.gz && \
    tar -zxf rarlinux-x64-${RAR_VERSION}.tar.gz && \
    cp rar/rar rar/unrar /tools-binaries/ && \
    rm -rf rar rarlinux-x64-${RAR_VERSION}.tar.gz

# DuckDB
RUN curl -L https://github.com/duckdb/duckdb/releases/download/v${DUCKDB_VERSION}/duckdb_cli-linux-amd64.zip -o duckdb.zip && \
    unzip -q duckdb.zip -d /tools-binaries && \
    rm duckdb.zip

# miller
RUN curl -L https://github.com/johnkerl/miller/releases/download/v${MILLER_VERSION}/miller-${MILLER_VERSION}-linux-amd64.tar.gz | tar xzf - && \
    mv miller-${MILLER_VERSION}-linux-amd64/mlr /tools-binaries/ && \
    rm -rf miller-${MILLER_VERSION}-linux-amd64

# dasel
RUN curl -sSLf "https://github.com/TomWright/dasel/releases/download/v${DASEL_VERSION}/dasel_linux_amd64" -o /tools-binaries/dasel

# ripgrep-all
RUN curl -L https://github.com/phiresky/ripgrep-all/releases/download/v${RGA_VERSION}/ripgrep_all-v${RGA_VERSION}-x86_64-unknown-linux-musl.tar.gz | tar xzf - && \
    mv ripgrep_all-v${RGA_VERSION}-x86_64-unknown-linux-musl/rga* /tools-binaries/ && \
    rm -rf ripgrep_all-v${RGA_VERSION}-x86_64-unknown-linux-musl

# hurl
RUN curl -L https://github.com/Orange-OpenSource/hurl/releases/download/${HURL_VERSION}/hurl-${HURL_VERSION}-x86_64-unknown-linux-gnu.tar.gz | tar xzf - && \
    mv hurl-${HURL_VERSION}-x86_64-unknown-linux-gnu/bin/* /tools-binaries/ && \
    rm -rf hurl-${HURL_VERSION}-x86_64-unknown-linux-gnu

# comby
RUN curl -L https://github.com/comby-tools/comby/releases/download/${COMBY_VERSION}/comby-${COMBY_VERSION}-x86_64-linux.tar.gz | tar xzf - && \
    mv comby-${COMBY_VERSION}-x86_64-linux /tools-binaries/comby

# ripgrep
RUN curl -L https://github.com/BurntSushi/ripgrep/releases/download/${RIPGREP_VERSION}/ripgrep-${RIPGREP_VERSION}-x86_64-unknown-linux-musl.tar.gz | tar xzf - && \
    mv ripgrep-${RIPGREP_VERSION}-x86_64-unknown-linux-musl/rg /tools-binaries/ && \
    rm -rf ripgrep-${RIPGREP_VERSION}-x86_64-unknown-linux-musl

# fd
RUN curl -L https://github.com/sharkdp/fd/releases/download/v${FD_VERSION}/fd-v${FD_VERSION}-x86_64-unknown-linux-musl.tar.gz | tar xzf - && \
    mv fd-v${FD_VERSION}-x86_64-unknown-linux-musl/fd /tools-binaries/ && \
    rm -rf fd-v${FD_VERSION}-x86_64-unknown-linux-musl

# sd
RUN curl -L https://github.com/chmln/sd/releases/download/v${SD_VERSION}/sd-v${SD_VERSION}-x86_64-unknown-linux-musl.tar.gz | tar xzf - && \
    mv sd-v${SD_VERSION}-x86_64-unknown-linux-musl/sd /tools-binaries/ && \
    rm -rf sd-v${SD_VERSION}-x86_64-unknown-linux-musl

# grex
RUN curl -L https://github.com/pemistahl/grex/releases/download/v${GREX_VERSION}/grex-v${GREX_VERSION}-x86_64-unknown-linux-musl.tar.gz | tar xzf - && \
    mv grex /tools-binaries/

# ouch
RUN curl -L https://github.com/ouch-org/ouch/releases/download/${OUCH_VERSION}/ouch-x86_64-unknown-linux-musl.tar.gz | tar xzf - && \
    mv ouch-x86_64-unknown-linux-musl/ouch /tools-binaries/ && \
    rm -rf ouch-x86_64-unknown-linux-musl

# watchexec
RUN curl -L https://github.com/watchexec/watchexec/releases/download/v${WATCHEXEC_VERSION}/watchexec-${WATCHEXEC_VERSION}-x86_64-unknown-linux-musl.tar.xz | tar xJf - && \
    mv watchexec-${WATCHEXEC_VERSION}-x86_64-unknown-linux-musl/watchexec /tools-binaries/ && \
    rm -rf watchexec-${WATCHEXEC_VERSION}-x86_64-unknown-linux-musl

# skim
RUN curl -L https://github.com/skim-rs/skim/releases/download/v${SKIM_VERSION}/skim-x86_64-unknown-linux-musl.tar.xz | tar xJf - -C /tools-binaries

# ast-grep
RUN curl -L https://github.com/ast-grep/ast-grep/releases/download/${AST_GREP_VERSION}/app-x86_64-unknown-linux-gnu.zip -o ast-grep.zip && \
    unzip -q ast-grep.zip -d /tmp && \
    mv /tmp/sg /tools-binaries/ && \
    rm -rf ast-grep.zip

# git-delta
RUN curl -L https://github.com/dandavison/delta/releases/download/${DELTA_VERSION}/delta-${DELTA_VERSION}-x86_64-unknown-linux-musl.tar.gz | tar xzf - && \
    mv delta-${DELTA_VERSION}-x86_64-unknown-linux-musl/delta /tools-binaries/ && \
    rm -rf delta-${DELTA_VERSION}-x86_64-unknown-linux-musl

# Make all binaries executable
RUN chmod +x /tools-binaries/*

# ==============================================================================
# FINAL STAGE
# ==============================================================================
FROM python:3.14-slim

# Install system tools and runtime libraries
RUN apt-get update && apt-get install -y \
    curl unzip bzip2 wget xz-utils aria2 \
    jq git bash ca-certificates \
    libpcre2-8-0 libev4 \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Copy all pre-compiled binaries from tools-builder stage
COPY --from=tools-builder /tools-binaries/* /usr/local/bin/

# Install Node.js
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y nodejs && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# uv (fast Python package manager)
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:${PATH}"

# Global npm tools
RUN npm install -g pnpm typescript tsx

# Python packages
RUN pip install --no-cache-dir requests beautifulsoup4 pandas duckdb

WORKDIR /workspace
