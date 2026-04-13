FROM alpine:3.21
ARG SQLITE_VERSION=3.48.0-r4

RUN apk add --no-cache sqlite=${SQLITE_VERSION}

COPY scripts/backup-keystore.sh /scripts/backup-keystore.sh
RUN chmod +x /scripts/backup-keystore.sh

ENTRYPOINT ["/bin/sh", "-c"]
