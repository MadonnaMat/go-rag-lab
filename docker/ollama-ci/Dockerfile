# A pre-baked, CPU-only Ollama image for CI: nomic-embed-text and
# qwen3:0.6b are pulled at *build* time, not container-start time, so CI
# never waits on a model download — ghcr.io/madonnamat/
# go-rag-lab-ollama-ci ships with the weights already in a layer.
# nomic-embed-text is the real production embedding model (not a smaller
# substitute) since our schema hardcodes vector(768) to match its output
# size — a different model would need a different dimension and simply
# wouldn't fit the column. qwen3:0.6b, by contrast, is deliberately NOT
# the production chat model (qwen3:8b, ~5.2GB) — CI only needs to prove
# tool-calling wiring works end-to-end (scripts/ci-verify's /chat smoke
# step), not exercise a production-quality model, so a much smaller
# tool-calling-capable model keeps this image's size/build time down.
#
# The official ollama/ollama image bundles CUDA, ROCm, and Vulkan GPU
# backends (~4GB combined) that GitHub Actions' GPU-less runners can never
# use — CI only ever needs the CPU backend. Rather than rebuilding Ollama
# from source (a much heavier, slower build requiring the full C++/Go
# toolchain), this multi-stage build treats the official image purely as a
# COPY source and cherry-picks only the CPU-relevant files into a fresh
# minimal base, dropping cuda_v12/, cuda_v13/, mlx_cuda_v13/, and vulkan/
# entirely (those four directories were ~4.3GB out of the upstream image's
# ~4.3GB /usr/lib/ollama — nearly all of it).
#
# This works because ollama/ollama's own Dockerfile declares no VOLUME for
# its model storage directory (verified against ollama/ollama's Dockerfile
# on GitHub before relying on this) — if it did, anything written during
# the RUN below would be discarded rather than committed into this image's
# layers, the same well-known gotcha that applies to e.g. postgres's
# official image and /var/lib/postgresql/data.
#
# Rebuild and push this (see CLAUDE.md) if the embedding model ever
# changes, or periodically to pick up Ollama updates.
FROM ollama/ollama:latest AS upstream

FROM ubuntu:24.04
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libopenblas0 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

COPY --from=upstream /bin/ollama /usr/bin/ollama
# CPU backend variants (one per x86-64 microarchitecture level, ~1MB each)
# plus the shared core libraries every backend needs — none of these live
# under a GPU-specific subdirectory, unlike cuda_v12/ etc.
COPY --from=upstream /usr/lib/ollama/libggml-cpu-*.so /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/libggml-base.so* /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/libggml.so* /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/libgomp.so* /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/libllama.so* /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/libllama-common.so* /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/libllama-server-impl.so /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/libllama-quantize-impl.so /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/libmtmd.so* /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/llama-server /usr/lib/ollama/
COPY --from=upstream /usr/lib/ollama/llama-quantize /usr/lib/ollama/

ENV OLLAMA_HOST=0.0.0.0:11434
EXPOSE 11434
ENTRYPOINT ["/usr/bin/ollama"]
CMD ["serve"]

# curl is installed and removed within this single RUN so it leaves no
# trace in the final image layer — it's only needed to prove the embed
# codepath actually works at build time. `ollama pull` alone only proves
# the server starts and the weights download; it never exercises
# inference, so a CPU backend lib missing only for embedding (as opposed
# to server startup) would otherwise go unnoticed until a real consumer
# (e.g. `make ci-verify`) hits it later.
RUN apt-get update && apt-get install -y --no-install-recommends curl && \
    ollama serve & \
    for i in $(seq 1 60); do ollama list >/dev/null 2>&1 && break; sleep 1; done && \
    ollama pull nomic-embed-text && \
    curl -fsS -X POST http://localhost:11434/api/embed \
      -H "Content-Type: application/json" \
      -d '{"model":"nomic-embed-text","input":"build-time smoke test"}' \
      | grep -q '"embeddings"' && \
    ollama pull qwen3:0.6b && \
    curl -fsS -X POST http://localhost:11434/api/chat \
      -H "Content-Type: application/json" \
      -d '{"model":"qwen3:0.6b","stream":false,"messages":[{"role":"user","content":"say hi"}]}' \
      | grep -q '"message"' && \
    apt-get purge -y curl && apt-get autoremove -y && apt-get clean && rm -rf /var/lib/apt/lists/*
