FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
ARG SQLITE_VERSION=3.53.4-r0

RUN apk add --no-cache sqlite=${SQLITE_VERSION}

COPY scripts/backup-keystore.sh /scripts/backup-keystore.sh
RUN chmod +x /scripts/backup-keystore.sh

ENTRYPOINT ["/bin/sh", "-c"]
