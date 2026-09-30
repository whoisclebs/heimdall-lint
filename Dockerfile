# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/heimdall ./cmd/heimdall

# The binary is static, so the runtime image contains nothing else.
FROM scratch
COPY --from=build /out/heimdall /heimdall
WORKDIR /workspace
USER 65534:65534
ENTRYPOINT ["/heimdall"]
CMD ["lint"]
