ARG GO_VERIFIER=golang:1.26-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514
FROM ${GO_VERIFIER}

ENV GOTOOLCHAIN=local GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org
COPY source/prometheus-source.tar.gz /opt/reference/source.tar.gz
RUN mkdir -p /opt/reference/source \
    && tar -xzf /opt/reference/source.tar.gz -C /opt/reference/source --strip-components=1 \
    && cd /opt/reference/source \
    && cp go.mod /opt/reference/go.mod \
    && cp go.sum /opt/reference/go.sum \
    && GOWORK=off go mod download all \
    && GOWORK=off GOFLAGS=-mod=mod go test -run '^$' ./tsdb/chunkenc ./tsdb ./promql/promqltest
COPY test.sh candidate_program.go candidate_reader.go feature_bridge.go build-storage-probe.sh legacy_writer.go controller.go /tests/
COPY common/ /tests/common/
RUN groupadd --gid 65532 micro1verifier \
    && useradd --uid 65532 --gid 65532 --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin micro1verifier \
    && chmod 0555 /tests/common/prepare-candidate.sh /tests/common/hardened-run.sh /tests/common/run-candidate.sh \
    && mkdir -p /app/verifier-artifacts /logs/verifier

# Cache the dependency graph selected by the upstream workspace as well.
RUN cd /opt/reference/source && if test -f go.work; then GOWORK=/opt/reference/source/go.work GOFLAGS='' go mod download all; fi
