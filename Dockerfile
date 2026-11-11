# syntax=docker/dockerfile:1

# One image per service:
#   docker build --build-arg SERVICE=api .     (also relay, worker)
# The result is a static binary on distroless, running as a non-root user.

ARG GO_VERSION=1.27

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG SERVICE
RUN test -n "$SERVICE" || { echo "set --build-arg SERVICE=api, relay or worker" >&2; exit 1; }
WORKDIR /src

# Download modules in their own layer so code changes don't refetch them.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/service "./cmd/$SERVICE"

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/service /service
# distroless's nonroot user, by number, so Kubernetes' runAsNonRoot check can verify it.
USER 65532:65532
ENTRYPOINT ["/service"]
