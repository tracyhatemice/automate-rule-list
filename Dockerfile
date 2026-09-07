# syntax=docker/dockerfile:1
# The builder runs on the build host's platform and cross-compiles for the
# target, so arm64 images do not need QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1-alpine AS build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/rulegen ./cmd/rulegen

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/rulegen /rulegen
WORKDIR /work
ENTRYPOINT ["/rulegen"]
CMD ["run", "-c", "/work/rulegen.yaml"]
