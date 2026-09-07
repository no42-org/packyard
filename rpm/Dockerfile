# Debian-based nginx rather than Alpine: createrepo_c is only packaged in
# Alpine edge/testing, while Debian ships createrepo-c as a regular package.
FROM nginx:1.31-trixie@sha256:05b8cb60c354a44ab824ea6e7dc69b46d50762cdbe728a347a5b656e6fb3d7c4
# createrepo-c lets promotion workflows publish RPMs inside this container
# (`docker compose exec --user 0 rpm /scripts/add-package.sh ...`) instead of
# needing the tooling and write access to the volume on the host.
# rpm-common provides the rpmrc/macros librpm reads when parsing packages.
RUN apt-get update && apt-get install -y --no-install-recommends createrepo-c rpm-common \
    && rm -rf /var/lib/apt/lists/*
COPY nginx.conf /etc/nginx/nginx.conf
COPY scripts/init-rpm-tree.sh /docker-entrypoint.d/10-init-rpm-tree.sh
COPY scripts/add-package.sh scripts/rebuild-metadata.sh /scripts/
RUN chmod +x /docker-entrypoint.d/10-init-rpm-tree.sh /scripts/*.sh \
    && chown -R nginx:nginx /usr/share/nginx/html
