FROM golang:1.8
RUN go get -u github.com/Masterminds/glide
RUN git clone https://github.com/lightningnetwork/lnd.git $GOPATH/src/github.com/lightningnetwork/lnd
WORKDIR $GOPATH/src/github.com/lightningnetwork/lnd
RUN glide install
RUN go install . ./cmd/...
