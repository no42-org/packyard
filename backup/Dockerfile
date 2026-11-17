FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
ARG SQLITE_VERSION=3.53.4-r0

RUN apk add --no-cache sqlite=${SQLITE_VERSION}

COPY scripts/backup-keystore.sh /scripts/backup-keystore.sh
RUN chmod +x /scripts/backup-keystore.sh

ENTRYPOINT ["/bin/sh", "-c"]
