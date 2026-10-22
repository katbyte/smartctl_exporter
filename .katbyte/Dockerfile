# The image goreleaser publishes to ghcr.io/katbyte/smartctl_exporter on a release tag: the build context holds the prebuilt program per platform,
# so this only adds smartctl and copies it onto alpine. upstream's Dockerfile expects promu's .build/ output instead, so it is left as it is.
# Like upstream's image it runs as nobody; smartctl needs root to read disks, so run it with user: root and privileged: true as upstream's README shows.
# alpine comes from Google's mirror of Docker Hub, which does not turn a shared release runner away for having pulled too much.
FROM mirror.gcr.io/library/alpine:3.24
RUN apk add --no-cache smartmontools
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/smartctl_exporter /bin/smartctl_exporter
EXPOSE 9633
USER nobody
ENTRYPOINT ["/bin/smartctl_exporter"]
