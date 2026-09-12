# Build stage: compile a fully static binary with all assets embedded.
# TARGETOS/TARGETARCH are set by buildx for multi-platform builds, so CI
# cross-compiles instead of emulating; they are empty for plain local builds.
FROM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" go build -trimpath -ldflags="-s -w" -o /out/paste ./cmd/paste \
	# A data dir owned by uid 65532 so the runtime nonroot user can write the
	# database into the /data volume created from it.
	&& mkdir -p /out/data \
	&& chown 65532:65532 /out/data

# Final image: distroless static, nonroot, binary only (assets are embedded).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/paste /paste
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot
EXPOSE 8080
VOLUME /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/paste", "healthcheck"]
ENTRYPOINT ["/paste"]
