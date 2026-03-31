FROM nginx:alpine
COPY nginx.conf /etc/nginx/nginx.conf
COPY scripts/init-rpm-tree.sh /docker-entrypoint.d/10-init-rpm-tree.sh
RUN chmod +x /docker-entrypoint.d/10-init-rpm-tree.sh
