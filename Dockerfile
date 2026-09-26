# go-signal container image: the static binary from `just build-static` on scratch.
#
#   docker run --rm -it -v go-signal:/data ghcr.io/cwbudde/go-signal link
#   docker run --rm -v go-signal:/data ghcr.io/cwbudde/go-signal receive
#
# The build context must hold dist/linux_<arch>/go-signal for each target arch.

# Runs on the build platform: it only collects files, so no emulation is needed for arm64.
FROM --platform=$BUILDPLATFORM alpine:3 AS certs
RUN apk add --no-cache ca-certificates && mkdir -p /data /config /tmp && chmod 1777 /tmp

FROM scratch
ARG TARGETARCH
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=certs --chown=65532:65532 /data /data
COPY --from=certs --chown=65532:65532 /config /config
COPY --from=certs /tmp /tmp
COPY dist/linux_${TARGETARCH}/go-signal /go-signal
USER 65532:65532
ENV HOME=/data XDG_DATA_HOME=/data XDG_CONFIG_HOME=/config
VOLUME /data
ENTRYPOINT ["/go-signal"]
