FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
ARG APTLY_VERSION=1.6.2

# bzip2 and xz-utils: aptly publish shells out to them to compress index files.
RUN apt-get update && apt-get install -y --no-install-recommends \
    bzip2 \
    ca-certificates \
    curl \
    gnupg \
    unzip \
    xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Download the architecture-appropriate aptly binary from GitHub releases.
# dpkg --print-architecture returns amd64 or arm64, matching the release naming.
# Use the exact versioned directory path to avoid extracting the bash completion
# script (also named aptly) alongside the binary.
RUN ARCH=$(dpkg --print-architecture) && \
    curl -fsSL \
      "https://github.com/aptly-dev/aptly/releases/download/v${APTLY_VERSION}/aptly_${APTLY_VERSION}_linux_${ARCH}.zip" \
      -o /tmp/aptly.zip && \
    unzip -j /tmp/aptly.zip \
      "aptly_${APTLY_VERSION}_linux_${ARCH}/aptly" \
      -d /usr/local/bin/ && \
    chmod +x /usr/local/bin/aptly && \
    rm /tmp/aptly.zip

RUN mkdir -p /opt/aptly

# Keep the container running so promotion scripts can exec into it.
CMD ["tail", "-f", "/dev/null"]
