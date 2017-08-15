FROM golang:1.8
RUN apt update && apt install -y patch
RUN go get -u github.com/Masterminds/glide
# aug 11 2017 
ENV point 48be9261ef5fa5eaf59b3b952474fd53f81eacf9
RUN git clone https://github.com/lightningnetwork/lnd.git $GOPATH/src/github.com/lightningnetwork/lnd
WORKDIR $GOPATH/src/github.com/lightningnetwork/lnd
RUN git checkout $point
ADD noTLS.patch .
RUN patch -p1 < noTLS.patch
ADD dumpInputSig.patch .
RUN patch -p1 < dumpInputSig.patch
RUN glide install
RUN go install . ./cmd/...
