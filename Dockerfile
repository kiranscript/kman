# The kman image (SPEC §15). GoReleaser's dockers_v2 builds it from the
# released linux binaries: the build context holds linux/<arch>/kman, so this
# file does not work with a plain `docker build .`.
#
# The base is pinned by digest; Dependabot proposes digest updates.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/kman /usr/bin/kman
USER 65532:65532
ENTRYPOINT ["/usr/bin/kman"]
