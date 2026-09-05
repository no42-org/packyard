FROM nginx:1.31-alpine@sha256:72ba65eb42c10344912a84ff42408db7d34f2feb642204570ab8fc5ffd29f1d3
COPY nginx.conf /etc/nginx/nginx.conf
COPY scripts/init-rpm-tree.sh /docker-entrypoint.d/10-init-rpm-tree.sh
RUN chmod +x /docker-entrypoint.d/10-init-rpm-tree.sh \
    && chown -R nginx:nginx /usr/share/nginx/html
