FROM golang:1.26.4-bookworm@sha256:b305420a68d0f229d91eb3b3ed9e519fcf2cf5461da4bef997bf927e8c0bfd2b
ENV GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS="-mod=vendor -p=2" CGO_ENABLED=0 GOCACHE=/opt/go-build-cache
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git patch python3 && rm -rf /var/lib/apt/lists/*
COPY source/victoriametrics-source.tar.gz source/SHA256SUMS /opt/input/
RUN cd /opt/input && sha256sum -c SHA256SUMS && mkdir -p /app/victoriametrics && tar -xzf victoriametrics-source.tar.gz -C /app/victoriametrics --strip-components=1
WORKDIR /app/victoriametrics
RUN go build -trimpath -buildvcs=false -o /usr/local/bin/victoria-metrics ./app/victoria-metrics && go test -run '^$' ./app/vmselect/promql
RUN useradd -m -u 1000 -s /bin/bash agent && chown -R agent:agent /app/victoriametrics /opt/go-build-cache && mkdir -p /app/verifier-artifacts
COPY runtime/ /opt/task-environment/
RUN install -m 0755 /opt/task-environment/task-env /usr/local/bin/task-env && \
    chmod 0755 /opt/task-environment/boot.sh && \
    mkdir -p /app/task-runtime/bin && \
    cp /usr/local/bin/victoria-metrics /app/task-runtime/bin/victoria-metrics && \
    cp /opt/task-environment/ENVIRONMENT.md /app/ENVIRONMENT.md && \
    chown -R agent:agent /app/task-runtime
RUN ln -s /usr/local/go/bin/go /usr/local/bin/go && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt
USER root
ENTRYPOINT ["/opt/task-environment/boot.sh"]
CMD ["sleep", "infinity"]
