FROM golang:1.8
RUN apt update && apt install -y patch
RUN go get -u github.com/Masterminds/glide
# aug 3 2017 
ENV point 59f906521371933f652cd75b7b49ce4bda99b481
RUN git clone https://github.com/lightningnetwork/lnd.git $GOPATH/src/github.com/lightningnetwork/lnd
WORKDIR $GOPATH/src/github.com/lightningnetwork/lnd
RUN git checkout $point
ADD noTLS.patch .
RUN patch -p1 < noTLS.patch
ADD dumpInputSig.patch .
RUN patch -p1 < dumpInputSig.patch
RUN glide install
RUN go install . ./cmd/...
