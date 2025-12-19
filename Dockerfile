# Simple single-stage build - no compilation needed!
FROM python:3.11-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    --no-install-recommends \
    ca-certificates \
    git \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

# Install uv
RUN pip install --no-cache-dir uv

# Create non-root user
RUN useradd --create-home --shell /bin/bash --uid 1000 podping

# Set working directory
WORKDIR /app

# Copy workspace files
COPY pyproject.toml ./
COPY uv.lock ./
COPY README.md ./
COPY asif asif
COPY podcast_index podcast_index
COPY src/ ./src/

# Install dependencies
RUN uv sync --frozen

# Copy entrypoint script
COPY entrypoint.sh ./
RUN chmod +x /app/entrypoint.sh

# Change ownership
RUN chown -R podping:podping /app

# Switch to non-root user
USER podping

# Set environment variables
ENV PYTHONPATH=/app/src
ENV PYTHONUNBUFFERED=1
ENV PATH="/app/.venv/bin:$PATH"

# Use entrypoint script
ENTRYPOINT ["/app/entrypoint.sh"]
