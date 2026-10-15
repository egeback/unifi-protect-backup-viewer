# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

FROM golang:1.27-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/unifi-protect-backup-viewer .
RUN CGO_ENABLED=0 go build -o /out/hashpw ./cmd/hashpw

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587

# intel-media-va-driver-non-free (iHD) is needed for full QuickSync codec
# coverage on newer Intel iGPUs (the free intel-media-va-driver's coverage
# is less reliable) — non-free/contrib aren't enabled by default. The base
# image uses the deb822 sources format, so add the components to its
# existing source stanzas rather than creating a second, conflicting
# classic-format entry for the same repo (apt rejects that: "Conflicting
# values set for option Signed-By").
RUN sed -i 's/^Components: main$/Components: main contrib non-free non-free-firmware/' /etc/apt/sources.list.d/debian.sources \
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
