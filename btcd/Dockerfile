FROM golang:1.8
RUN apt update && apt install -y patch
RUN go get -u github.com/Masterminds/glide
RUN git clone https://github.com/roasbeef/btcd.git $GOPATH/src/github.com/roasbeef/btcd
WORKDIR $GOPATH/src/github.com/roasbeef/btcd
ADD patch .
RUN patch -p1 < patch
RUN glide install
#ENV GOPATH /go/src/github.com/roasbeef/btcd/vendor:$GOPATH
#ENV GOPATH $GOPATH/github.com/Roasbeef/btcd/vendor
#RUN find . -name siphash
RUN go install . ./cmd/...
RUN mkdir /root/.btcctl
ADD btcctl.conf /root/.btcctl/
