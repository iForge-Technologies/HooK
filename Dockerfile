# syntax=docker/dockerfile:1
# Build stages run on the builder's own platform and cross-compile, so a
# multi-arch build (amd64 + arm64) needs no emulation.

FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS hub
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" -o /hub ./cmd/hub
# An empty data folder for the unprivileged user (uid 65532) to own: Docker
# volumes start with the image's copy, so the hub can write to them.
RUN mkdir -p /data
# The agents the hub hands out through install.sh, for both server types.
RUN for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w" -o /agents/hook-agent-linux-$arch ./cmd/agent; \
    done

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=hub /hub /hub
COPY --from=hub /agents /agents
COPY --from=hub --chown=65532:65532 /data /data
ENV HOOK_DATA=/data HOOK_ADDR=:8080 HOOK_AGENT_DIR=/agents
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/hub"]
