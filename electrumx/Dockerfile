FROM python:3.6-jessie

RUN apt-get update && apt-get install -y libleveldb-dev

WORKDIR /
RUN wget https://github.com/kyuupichan/electrumx/archive/3f9e2363c2ddbded7e0d9431e578855c5f54887b.tar.gz
RUN tar xzf *.tar.gz
RUN rm *.tar.gz
RUN mv electrumx-* electrumx
ADD simnet.patch /
RUN cd electrumx && patch -p1 < ../simnet.patch
RUN cd electrumx && python3 setup.py install
RUN adduser electrumx
RUN bash -c "echo -e 'password\npassword\n' | passwd electrumx"
