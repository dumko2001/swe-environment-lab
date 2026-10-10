FROM golang:1.26.4-bookworm@sha256:b305420a68d0f229d91eb3b3ed9e519fcf2cf5461da4bef997bf927e8c0bfd2b
ENV GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS="-mod=vendor -p=2" CGO_ENABLED=0 GOCACHE=/opt/go-build-cache
RUN apt-get update && apt-get install -y --no-install-recommends python3 util-linux && rm -rf /var/lib/apt/lists/*
COPY source/victoriametrics-source.tar.gz source/SHA256SUMS /opt/input/
RUN cd /opt/input && sha256sum -c SHA256SUMS && mkdir -p /opt/reference/source && tar -xzf victoriametrics-source.tar.gz -C /opt/reference/source --strip-components=1 && cd /opt/reference/source && go build -trimpath -buildvcs=false -o /opt/reference/victoria-metrics ./app/victoria-metrics
COPY test.sh prepare.py controller.py /tests/
RUN groupadd -g 65532 candidate && useradd -u 65532 -g 65532 -M -s /usr/sbin/nologin candidate && mkdir -p /app/verifier-artifacts /logs/verifier && chmod 0700 /tests /opt/reference /opt/go-build-cache /logs /app/verifier-artifacts && chmod 0555 /tests/test.sh
