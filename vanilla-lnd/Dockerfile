FROM golang:1.8
RUN apt update && apt install -y patch
RUN go get -u github.com/Masterminds/glide
RUN git clone https://github.com/lightningnetwork/lnd.git $GOPATH/src/github.com/lightningnetwork/lnd
WORKDIR $GOPATH/src/github.com/lightningnetwork/lnd
ADD patch .
RUN patch -p1 < patch
RUN glide install
RUN go install . ./cmd/...
