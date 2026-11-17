# Build on the native platform and cross-compile for the target one: no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27.4 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/okf-operator ./cmd/okf-operator

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/okf-operator /usr/local/bin/okf-operator
ENTRYPOINT ["/usr/local/bin/okf-operator"]
