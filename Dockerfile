# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

ARG GO_VERSION=1.26.5
ARG NODE_VERSION=24.19.0
ARG NPM_VERSION=12.0.2
ARG VITE_PLUS_VERSION=0.2.6

FROM node:${NODE_VERSION}-bookworm@sha256:934240a162082fd8b8a2f90cd5114446443f1eba1c5378f6687167ca405e6584 AS frontend-build

ARG NPM_VERSION
ARG VITE_PLUS_VERSION
WORKDIR /src/web

RUN npm install --global --no-fund --no-audit npm@${NPM_VERSION} vite-plus@${VITE_PLUS_VERSION}
COPY web/package.json web/package-lock.json ./
RUN vp install --frozen-lockfile
COPY web/ ./
RUN vp build

FROM golang:${GO_VERSION}-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS backend-build

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=frontend-build /src/web/dist ./web/dist
RUN rm -rf internal/webui/dist/* \
    && cp -R web/dist/. internal/webui/dist/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath \
    -ldflags "-X github.com/lorenzocorallo/megadw/internal/buildinfo.Version=${VERSION} -X github.com/lorenzocorallo/megadw/internal/buildinfo.Commit=${COMMIT} -X github.com/lorenzocorallo/megadw/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/megad ./cmd/megad

# The release stage contains only the self-contained Go process and the CA
# bundle supplied by the pinned distroless base. Node.js, Java, shells, and
# package managers are build-time dependencies only.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

COPY --from=backend-build /out/megad /usr/local/bin/megad
USER 65532:65532
ENV MEGAD_LISTEN=0.0.0.0:8080
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD ["/usr/local/bin/megad", "-healthcheck"]
ENTRYPOINT ["/usr/local/bin/megad"]
