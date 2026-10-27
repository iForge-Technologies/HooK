# syntax=docker/dockerfile:1
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS hub
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /hub ./cmd/hub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=hub /hub /hub
ENV HOOK_DATA=/data HOOK_ADDR=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/hub"]
