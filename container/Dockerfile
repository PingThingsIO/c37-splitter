FROM ubuntu:xenial as builder

ENV GO_VERSION 1.12.5
ENV GO_ARCH linux-amd64

RUN apt-get update && apt-get install -y net-tools git build-essential vim wget
RUN wget --no-verbose -O /tmp/go.tar.gz https://storage.googleapis.com/golang/go${GO_VERSION}.${GO_ARCH}.tar.gz
RUN tar -xf /tmp/go.tar.gz -C /usr/local/
WORKDIR /srv/go/src/github.com/PingThingsIO/c37splitter
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/local/go/bin:/srv/go/bin
ENV GOPATH=/srv/go
RUN go get -u github.com/golang/dep/cmd/dep
COPY . .
RUN dep ensure
RUN cd cmd/c37splitter && go build -v


FROM ubuntu:xenial

ENV GOGC=40
COPY --from=builder /srv/go/src/github.com/PingThingsIO/c37splitter/cmd/c37splitter/c37splitter /bin/c37splitter

ENTRYPOINT ["/bin/c37splitter", "/etc/c37splitter/config.yaml"]
