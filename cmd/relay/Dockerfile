# Run this dockerfile from the top level of the indigo git repository like:
#
#   podman build -f ./cmd/relay/Dockerfile -t relay .

### Compile stage
FROM golang:1.26.10-alpine AS build-env
RUN apk add --no-cache build-base make git

WORKDIR /dockerbuild

# Cache Go module dependencies separately for better layer caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy source code and build
COPY . .

# timezone data for alpine builds
# Builds without .git in the context (no git describe), e.g. from a submodule checkout.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -tags timetzdata -o /relay ./cmd/relay

### Build Frontend stage
FROM node:24-alpine AS web-builder

WORKDIR /app

# Copy package files first for better layer caching
COPY cmd/relay/relay-admin-ui/package.json cmd/relay/relay-admin-ui/package-lock.json ./

# Cache npm dependencies
RUN --mount=type=cache,target=/root/.npm \
    npm ci

# Copy source and build
COPY cmd/relay/relay-admin-ui/ ./

RUN npm run build

### Run stage
FROM alpine:3.24

RUN apk add --no-cache --update dumb-init ca-certificates runit
ENTRYPOINT ["dumb-init", "--"]

WORKDIR /
RUN mkdir -p data/relay
COPY --from=build-env /relay /
COPY --from=web-builder /app/dist/ public/

# small things to make golang binaries work well under alpine
ENV GODEBUG=netdns=go
ENV TZ=Etc/UTC

EXPOSE 2470

CMD ["/relay", "serve"]

LABEL org.opencontainers.image.source=https://github.com/bluesky-social/indigo
LABEL org.opencontainers.image.description="atproto Relay"
LABEL org.opencontainers.image.licenses="MIT OR Apache-2.0"
