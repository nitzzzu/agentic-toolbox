ARG REGISTRY=ghcr.io
ARG ORG=nitzzzu
ARG TAG=latest
FROM ${REGISTRY}/${ORG}/base:${TAG}

# Data processing: DuckDB, Polars, Pandas, Jupyter
RUN pip install --break-system-packages \
    duckdb polars pandas numpy \
    jupyterlab ipykernel

WORKDIR /workspace
