# The image GoReleaser builds: the static binary, with CA certificates to reach CronSpy.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/cronspy /usr/bin/cronspy
ENTRYPOINT ["/usr/bin/cronspy"]
