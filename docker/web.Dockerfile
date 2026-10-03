FROM node:22.14.0-alpine AS build
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM nginx:1.27.4-alpine
COPY docker/nginx/default.conf.template /etc/nginx/conf.d/default.conf
COPY docker/nginx/security-headers.conf /etc/nginx/snippets/pulse-security-headers.conf
COPY --from=build /app/dist/ /usr/share/nginx/html/
EXPOSE 8443/tcp
