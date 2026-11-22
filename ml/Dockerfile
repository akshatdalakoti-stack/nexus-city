# Inference service image. One image, one model per container:
#   nexus-ml serve --models traffic
# Trained artifacts are mounted at /models (see docker-compose.yml).
FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 NEXUS_MODELS_DIR=/models
WORKDIR /app
# Serving-only dependencies: training's pandas/pyarrow are pruned and Linux
# gets CPU-only xgboost. Regenerate with:
#   uv export --frozen --no-dev --no-emit-project --no-hashes --prune pandas --prune pyarrow -o requirements.lock.txt
COPY requirements.lock.txt .
RUN pip install --no-cache-dir -r requirements.lock.txt
COPY pyproject.toml README.md ./
COPY src ./src
RUN pip install --no-cache-dir --no-deps .
RUN useradd --create-home --uid 10001 app
USER app
EXPOSE 8090
HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/health')"
CMD ["nexus-ml", "serve", "--models", "traffic,energy,pm25,anomaly", "--port", "8090"]
