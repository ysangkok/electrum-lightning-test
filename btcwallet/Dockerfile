FROM golang:1.8
RUN go get -u github.com/Masterminds/glide
RUN git clone https://github.com/btcsuite/btcwallet.git $GOPATH/src/github.com/btcsuite/btcwallet
WORKDIR $GOPATH/src/github.com/btcsuite/btcwallet
RUN glide install
RUN apt update && apt install -y patch
ADD patch .
RUN patch -p1 < patch
RUN go install . ./cmd/...
