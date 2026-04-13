FROM alpine:3.21
ARG BACKUP_VERSION=1.0.0

RUN apk add --no-cache sqlite

COPY scripts/backup-keystore.sh /scripts/backup-keystore.sh
RUN chmod +x /scripts/backup-keystore.sh

ENTRYPOINT ["/bin/sh", "-c"]
