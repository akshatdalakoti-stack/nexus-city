# City Intelligence assistant. Without LLM_API_KEY it answers in rule-based mode.
FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 ASSISTANT_HOST=0.0.0.0
WORKDIR /app
COPY requirements.lock.txt .
RUN pip install --no-cache-dir -r requirements.lock.txt
COPY pyproject.toml README.md ./
COPY src ./src
RUN pip install --no-cache-dir --no-deps .
RUN useradd --create-home --uid 10001 app
USER app
EXPOSE 8095
HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8095/health')"
CMD ["nexus-assistant", "serve", "--port", "8095"]
