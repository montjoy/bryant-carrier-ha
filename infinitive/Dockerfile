# Build the daemon from the source vendored in this repository.
#
# The previous add-on ran `apk add go` and `go install github.com/...@latest` at
# image build time, which meant the shipped binary depended on whatever Alpine's
# Go package and the upstream default branch happened to be that day.  Compiling
# pinned source in a separate stage makes the build reproducible and ties the
# daemon version to the add-on version.
ARG BUILD_FROM

FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first so edits to the source do not invalidate the module cache.
COPY daemon/go.mod daemon/go.sum ./
RUN go mod download

COPY daemon/ ./

ARG BUILD_VERSION=dev
# Everything in the dependency tree is pure Go, so a static binary drops the
# runtime image's dependency on the build stage's libc entirely.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w -X main.version=${BUILD_VERSION}" -o /infinitive .


FROM ${BUILD_FROM}

ENV LANG=C.UTF-8

COPY --from=build /infinitive /usr/bin/infinitive
COPY rootfs /

RUN chmod a+x \
  /usr/bin/infinitive \
  /etc/s6-overlay/s6-rc.d/infinitive/run \
  /etc/s6-overlay/s6-rc.d/infinitive/finish

# Labels are what the Supervisor reads back to identify the built image.
ARG BUILD_ARCH
ARG BUILD_DATE
ARG BUILD_DESCRIPTION="Carrier/Bryant Infinity control with Home Assistant MQTT discovery"
ARG BUILD_NAME="Infinitive"
ARG BUILD_REF
ARG BUILD_REPOSITORY
ARG BUILD_VERSION

LABEL \
  io.hass.name="${BUILD_NAME}" \
  io.hass.description="${BUILD_DESCRIPTION}" \
  io.hass.arch="${BUILD_ARCH}" \
  io.hass.type="addon" \
  io.hass.version="${BUILD_VERSION}" \
  org.opencontainers.image.source="https://github.com/${BUILD_REPOSITORY}" \
  org.opencontainers.image.created="${BUILD_DATE}" \
  org.opencontainers.image.revision="${BUILD_REF}" \
  org.opencontainers.image.version="${BUILD_VERSION}"
