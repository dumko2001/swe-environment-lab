ARG GO_VERIFIER=golang:1.26-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514
FROM ${GO_VERIFIER}

ENV GOTOOLCHAIN=local GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org
COPY source/prometheus-source.tar.gz /opt/reference/source.tar.gz
RUN mkdir -p /opt/reference/source \
    && tar -xzf /opt/reference/source.tar.gz -C /opt/reference/source --strip-components=1 \
    && cd /opt/reference/source \
    && go mod download \
    && go test -run '^$' ./cmd/prometheus ./scrape/...
COPY test.sh controller.go hidden_pool_identity_test.go hidden_client_retirement_test.go /tests/
COPY common/ /tests/common/
RUN groupadd --gid 65532 micro1verifier \
    && useradd --uid 65532 --gid 65532 --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin micro1verifier \
    && chmod 0555 /tests/common/prepare-candidate.sh /tests/common/hardened-run.sh \
    && cp /opt/reference/source/go.mod /opt/reference/go.mod \
    && cp /opt/reference/source/go.sum /opt/reference/go.sum \
    && mkdir -p /app/verifier-artifacts /logs/verifier
