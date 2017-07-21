FROM golang:1.8
RUN go get -u github.com/Masterminds/glide
RUN git clone https://github.com/roasbeef/btcd.git $GOPATH/src/github.com/roasbeef/btcd
WORKDIR $GOPATH/src/github.com/roasbeef/btcd
RUN glide install
#ENV GOPATH /go/src/github.com/roasbeef/btcd/vendor:$GOPATH
#ENV GOPATH $GOPATH/github.com/Roasbeef/btcd/vendor
#RUN find . -name siphash
RUN go install . ./cmd/...
