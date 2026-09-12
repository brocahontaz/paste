# Build stage: compile a fully static binary with all assets embedded.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/paste ./cmd/paste \
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
