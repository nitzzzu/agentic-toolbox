ARG REGISTRY=ghcr.io
ARG ORG=nitzzzu
ARG TAG=latest
FROM ${REGISTRY}/${ORG}/base:${TAG}

# Media processing: ffmpeg, Pillow, imagemagick
RUN apt-get update && apt-get install -y \
    ffmpeg imagemagick \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --break-system-packages Pillow yt-dlp cyberdrop-dl-patched

WORKDIR /workspace
