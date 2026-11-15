# Stage 1 builds the React site into plain HTML, CSS and JavaScript. Node is
# only needed here and is left out of the final image.
FROM node:22-alpine AS web
WORKDIR /web

# Install packages before copying the code, so Docker can reuse this step
# until package.json or package-lock.json change. npm ci installs the exact
# versions listed in package-lock.json.
COPY web/package.json web/package-lock.json ./
RUN npm ci

# Build the site into /web/dist.
COPY web/ .
RUN npm run build

# Stage 2 is Nginx, which serves the built site, forwards /api requests and
# chat to the Go API, and forwards /hls requests to the cache (see
# nginx.conf).
FROM nginx:1.27-alpine
COPY nginx/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=web /web/dist /usr/share/nginx/html
