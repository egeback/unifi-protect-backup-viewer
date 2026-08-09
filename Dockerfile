# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/unifi-protect-backup-viewer .
RUN CGO_ENABLED=0 go build -o /out/hashpw ./cmd/hashpw

FROM debian:bookworm-slim

# intel-media-va-driver-non-free (iHD) is needed for QuickSync HEVC decode +
# H.264 encode on the Alder Lake-N iGPU (the free intel-media-va-driver's
# codec coverage is less reliable for this generation) — non-free/contrib
# aren't enabled by default in the base image.
RUN echo "deb http://deb.debian.org/debian bookworm main contrib non-free non-free-firmware" > /etc/apt/sources.list.d/non-free.list \
	&& echo "deb http://deb.debian.org/debian-security bookworm-security main contrib non-free non-free-firmware" >> /etc/apt/sources.list.d/non-free.list \
	&& apt-get update \
	&& apt-get install --no-install-recommends -y \
		ffmpeg \
		intel-media-va-driver-non-free \
		vainfo \
		ca-certificates \
		curl \
	&& rm -rf /var/lib/apt/lists/*

RUN useradd --system --create-home --uid 1000 app
WORKDIR /app
COPY --from=build /out/unifi-protect-backup-viewer /out/hashpw ./

ENV LISTEN_ADDR=:8080 \
	NVR_PATH=/nvr \
	DATA_PATH=/data

EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
	CMD curl -fs http://localhost:8080/health || exit 1

USER app
ENTRYPOINT ["./unifi-protect-backup-viewer"]
