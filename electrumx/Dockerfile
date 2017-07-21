FROM python:3.6-jessie

RUN apt-get update && apt-get install -y libleveldb-dev

RUN wget https://github.com/kyuupichan/electrumx/archive/3f9e2363c2ddbded7e0d9431e578855c5f54887b.tar.gz
RUN tar xzf *.tar.gz
RUN rm *.tar.gz
RUN cd electrumx-* && python3 setup.py install
RUN openssl
CMD SSL_KEYFILE=key.pem SSL_CERTFILE=cert.pem TCP_PORT=50001 SSL_PORT=50002 RPC_PORT=8000 NET=simnet DAEMON_URL=http://doggman:donkey@127.0.0.1:18332 DB_DIRECTORY=$PWD/../electrumx-db electrumx_server
