FROM golang:1.26-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514
ENV DEBIAN_FRONTEND=noninteractive GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS="-mod=readonly -p=2 -tags=slicelabels"
RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-bcrypt memcached openssl && rm -rf /var/lib/apt/lists/*
COPY source/thanos-source.tar.gz /opt/reference/source.tar.gz
RUN mkdir -p /opt/reference/source && tar -xzf /opt/reference/source.tar.gz -C /opt/reference/source --strip-components=1 && cd /opt/reference/source && go mod download && go build -o /opt/reference/thanos ./cmd/thanos && cp go.mod go.sum /opt/reference/
COPY test.sh controller.py /tests/
COPY common/ /tests/common/
RUN groupadd -g 65532 micro1verifier && useradd -u 65532 -g 65532 -M -d /nonexistent -s /usr/sbin/nologin micro1verifier && chmod 0555 /tests/common/*.sh && mkdir -p /app/verifier-artifacts /logs/verifier
