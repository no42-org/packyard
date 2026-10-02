FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
ARG SQLITE_VERSION=3.53.4-r0

RUN apk add --no-cache sqlite=${SQLITE_VERSION}

COPY scripts/backup-keystore.sh /scripts/backup-keystore.sh
RUN chmod +x /scripts/backup-keystore.sh

ENTRYPOINT ["/bin/sh", "-c"]
