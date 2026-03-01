ARG REGISTRY=ghcr.io
ARG ORG=nitzzzu
ARG TAG=latest
FROM ${REGISTRY}/${ORG}/base:${TAG}

# Browser automation: Playwright + Chromium + crawl4ai
RUN apt-get update && apt-get install -y \
    chromium chromium-driver \
    fonts-liberation libatk-bridge2.0-0 libgtk-4-1 \
    libnss3 libxss1 libasound2 \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --break-system-packages playwright crawl4ai \
    && playwright install chromium --with-deps

WORKDIR /workspace
