# VaultRun Browser Image - Playwright Python
# Headless Chromium pre-installed for web automation and scraping

FROM python:3.12-slim

# Install system dependencies for Playwright
RUN apt-get update && apt-get install -y \
    wget \
    gnupg \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install Playwright and browsers
RUN pip install --no-cache-dir playwright==1.40.0 && \
    playwright install chromium && \
    playwright install-deps chromium

# Create workspace directory
RUN mkdir -p /workspace && chown nobody:nogroup /workspace

# Set working directory
WORKDIR /workspace

# Default user (non-root)
USER nobody

# Health check - verify browser is installed
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD python3 -c "from playwright.sync_api import sync_playwright; p = sync_playwright().start(); p.chromium.launch(); p.stop()" || exit 1

# Default command
CMD ["/bin/bash"]

# Metadata
LABEL org.opencontainers.image.title="VaultRun Browser (Playwright Python)"
LABEL org.opencontainers.image.description="Python 3.12 with Playwright and headless Chromium"
LABEL org.opencontainers.image.vendor="VaultRun"
LABEL org.opencontainers.image.source="https://github.com/nickvd7/vaultrun"
