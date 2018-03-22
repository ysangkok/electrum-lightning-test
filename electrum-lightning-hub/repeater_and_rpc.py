import jsonrpc_base
from jsonrpc_async import Server
import asyncio

from h2.config import H2Configuration
from h2.connection import H2Connection
from h2.events import (
    ConnectionTerminated, DataReceived, RequestReceived, StreamEnded
)
from h2.errors import ErrorCodes
from h2.exceptions import ProtocolError

import grpc
from aiogrpc import secure_channel
from aiohttp import web

from lib.ln.electrum_bridge import rpc_pb2_grpc, rpc_pb2

import lib.ln.lnrpc.rpc_pb2 as lnrpc
import lib.ln.lnrpc.rpc_pb2_grpc as lnrpcgrpc

from google.protobuf import json_format

from typing import List, Tuple
from datetime import datetime
import time
import json
import io
import binascii
import collections
import traceback
import shlex
import tempfile
import os
import sys
from subprocess import DEVNULL, PIPE

import socksserver

from lncli_endpoint import create_on_loop

import logging
logging.basicConfig(format='%(asctime)s %(message)s', level=logging.INFO)

RequestData = collections.namedtuple('RequestData', ['headers', 'data'])

try:
  with open("key_to_port") as r:
    KEY_TO_PORT = json.loads(r.read())
except FileNotFoundError:
  KEY_TO_PORT = {}

class H2Protocol(asyncio.Protocol):
    def __init__(self, elec, killQueuePort):
        config = H2Configuration(client_side=False, header_encoding='utf-8')
        self.conn = H2Connection(config=config)
        self.transport = None
        self.stream_data = {}
        self.elec = elec
        self.killQueuePort = killQueuePort

    def connection_made(self, transport: asyncio.Transport):
        self.transport = transport
        self.conn.initiate_connection()
        self.transport.write(self.conn.data_to_send())

    def data_received(self, data: bytes):
        try:
            events = self.conn.receive_data(data)
        except ProtocolError as e:
            self.transport.write(self.conn.data_to_send())
            self.transport.close()
        else:
            self.transport.write(self.conn.data_to_send())
            for event in events:
                if isinstance(event, RequestReceived):
                    self.request_received(event.headers, event.stream_id)
                elif isinstance(event, DataReceived):
                    self.receive_data(event.data, event.stream_id)
                elif isinstance(event, StreamEnded):
                    self.stream_complete(event.stream_id)
                elif isinstance(event, ConnectionTerminated):
                    self.transport.close()

                self.transport.write(self.conn.data_to_send())

    def request_received(self, headers: List[Tuple[str, str]], stream_id: int):
        headers = collections.OrderedDict(headers)
        method = headers[':method']

        # We only support GET and POST.
        if method not in ('GET', 'POST'):
            self.return_405(headers, stream_id)
            return

        # Store off the request data.
        request_data = RequestData(headers, io.BytesIO())
        self.stream_data[stream_id] = request_data

    def return_405(self, headers: List[Tuple[str, str]], stream_id: int):
        """
        We don't support the given method, so we want to return a 405 response.
        """
        response_headers = (
            (':status', '405'),
            ('content-length', '0'),
            ('server', 'asyncio-h2'),
        )
        self.conn.send_headers(stream_id, response_headers, end_stream=True)

    def receive_data(self, data: bytes, stream_id: int):
        """
        We've received some data on a stream. If that stream is one we're
        expecting data on, save it off. Otherwise, reset the stream.
        """
        try:
            stream_data = self.stream_data[stream_id]
        except KeyError:
            self.conn.reset_stream(
                stream_id, error_code=ErrorCodes.PROTOCOL_ERROR
            )
        else:
            self.received_data = data
            stream_data.data.write(data)
    def stream_complete(self, stream_id: int):
      try:
          request_data = self.stream_data[stream_id]
      except KeyError:
          logging.info("probably already 405")
          # Just return, we probably 405'd this already
          return

      headers = request_data.headers
      body = request_data.data.getvalue()

      # ['FetchRootKey', 'ConfirmedBalance', 'ListUnspentWitness', 'NewAddress']
      methods = [x for x in rpc_pb2_grpc.ElectrumBridgeServicer.__dict__.keys() if not x.startswith("__")]

      path = dict(headers)[":path"]
      logging.info("PATH:" + path)

      for methodname in methods:
        if methodname in path:
          a = rpc_pb2.__dict__[methodname + "Request"]()
          # https://grpc.io/docs/guides/wire.html
          if body[0] != 0:
            logging.warning("compressed grpc message?")
            logging.warning(body)
            logging.warning(methodname + "Request")

          dec = int.from_bytes(body[1:5], byteorder="big")
          if dec + 5 != len(body):
            logging.warning("length mismatch?")
            logging.warning("{} {} {}".format(body[1:5], dec, len(body)))
          a.ParseFromString(body[5:])
          jso = json_format.MessageToJson(a)
          if len(json.loads(jso).keys()) == 0 and methodname == "FetchInputInfo":
            raise Exception("no keys" + repr(a) + " " + repr(body))
          res = getattr(self.elec, methodname)(jso)
          def done(fut):
            try:
              fut.exception()
              logging.info("result " + fut.result())
            except Exception as e:
              logging.warning("While handling " + methodname)
              traceback.print_exc()
              response_headers = (
                  (':status', '500'),
                  ('server', 'asyncio-h2'),
              )
              self.conn.send_headers(stream_id, response_headers, end_stream=True)
              data = self.conn.data_to_send()
              self.transport.write(data)
              asyncio.ensure_future(assoc[self.killQueuePort].killQueue.put(data))
              return
            b = rpc_pb2.__dict__[methodname + "Response"]()
            try:
              json_format.Parse(fut.result(), b)
            except:
              json_format.Parse("{}", b)
            bajts = b.SerializeToString()
            bajts = b"\x00" + len(bajts).to_bytes(byteorder="big", length=4) + bajts
            response_headers = (
                (':status', '200'),
                ('content-type', 'application/grpc+proto'),
                ('content-length', str(len(bajts))),
                ('server', 'asyncio-h2'),
            )
            self.conn.send_headers(stream_id, response_headers)
            self.conn.send_data(stream_id, bajts)
            self.conn.send_headers(stream_id, (("grpc-status", "0",),), end_stream=True)
            data = self.conn.data_to_send()
            self.transport.write(data)
            asyncio.ensure_future(assoc[self.killQueuePort].killQueue.put(data))
          asyncio.ensure_future(res).add_done_callback(done)
          return
      raise Exception("method " + path + " not found!")

servers = {}

def make_h2handler(port, killQueuePort):
  def handler():
    if port in servers:
      return servers[port]
    servers[port] = H2Protocol(Server("http://localhost:" + str(port)), killQueuePort)
    logging.info("made new client connecting to port " + str(port))
    return servers[port]
  return handler

def get_bitcoind_server():
    t = tempfile.NamedTemporaryFile(prefix="bitcoind_config", delete=False)
    t.write(b"""
regtest=1
txindex=1
printtoconsole=1
addrindex=1
rpcuser=doggman
rpcpassword=donkey
rpcbind=0.0.0.0
rpcallowip=127.0.0.1
""")
    t.flush()
    return asyncio.create_subprocess_shell("rm -rf /home/janus/.bitcoin/regtest && /home/janus/bitcoin-simnet/bin/bitcoind -conf=" + t.name, stdout=DEVNULL, stderr=DEVNULL)

def get_electrumx_server():
    os.environ["TCP_PORT"] = "50001"
    os.environ["SSL_PORT"] = "50002"
    os.environ["RPC_PORT"] = "8000"
    os.environ["NET"] = "simnet"
    os.environ["DAEMON_URL"] = "http://doggman:donkey@127.0.0.1:18332"
    os.environ["DB_DIRECTORY"] = "/home/janus/electrumx-db"
    os.environ["SSL_CERTFILE"] = "/home/janus/electrumx/cert.pem"
    os.environ["SSL_KEYFILE"] = "/home/janus/electrumx/key.pem"
    os.environ["COIN"] = "Bitcoin"
    return asyncio.create_subprocess_shell("rm -rf /home/janus/electrumx-db/ && mkdir /home/janus/electrumx-db && /home/janus/electrumx/electrumx_server.py", stdout=DEVNULL, stderr=DEVNULL)

def get_btcd_server(miningaddr):
    t = tempfile.NamedTemporaryFile(prefix="btcd_config", delete=False)
    t.write(b"""
      rpcuser=youruser
      rpcpass=SomeDecentp4ssw0rd
      simnet=1
      miningaddr=""" + miningaddr.encode("ascii") + b"""
      txindex=1
      addrindex=1
      rpclisten=127.0.0.1
    """)
    t.flush()
    datadir=tempfile.TemporaryDirectory(prefix="btcd_datadir")
    # Note that ~/.btcd is still used for e.g. the rpccert!
    cmd = "/home/janus/go/bin/btcd -C " + shlex.quote(t.name) + " --datadir " + shlex.quote(datadir.name) + " --connect localhost"
    return asyncio.create_subprocess_shell(cmd)

PEERPORTS_TO_PUBKEYS = {}

async def get_lnd_server(electrumport, peerport, rpcport, restport, silent, simnet, testnet, lnddir):
      assert simnet and not testnet or not simnet and testnet
      kwargs = {"stdout":DEVNULL, "stderr":DEVNULL} if silent else {}
      bitcoinrpcport = 18556 if simnet else 18334 # TODO does not support mainnet
      cmd = "~/go/bin/lnd --debuglevel warn --configfile=/dev/null --rpclisten=localhost:" + str(rpcport) + " --restlisten=localhost:" + str(restport) + " --lnddir=" + lnddir + " --listen=localhost:" + str(peerport) + " --bitcoin.active " + ("--bitcoin.simnet" if simnet else "") + ("--bitcoin.testnet" if testnet else "") + " --btcd.rpcuser=youruser --btcd.rpcpass=SomeDecentp4ssw0rd --btcd.rpchost=localhost:" + str(bitcoinrpcport) + " --noencryptwallet --electrumport " + str(electrumport)
      logging.info(cmd)
      lnd = await asyncio.create_subprocess_shell(cmd, **kwargs)

      cmd = "~/go/bin/lncli --lnddir=" + lnddir + " --rpcserver=localhost:" + str(rpcport) + " getinfo"
      while True:
        proc = await asyncio.create_subprocess_shell(cmd=cmd, stdout=PIPE, stderr=PIPE)
        try:
          await asyncio.wait_for(proc.wait(), 5)
          if proc.returncode == 0: break
          await asyncio.sleep(1)
        except asyncio.TimeoutError:
          logging.error("timeouterror")
      pubkey = json.loads(await proc.stdout.read())["identity_pubkey"]
      PEERPORTS_TO_PUBKEYS[peerport] = pubkey

      return lnd

locks = collections.defaultdict(asyncio.Lock)

def mkhandler(port):
  q = asyncio.Queue()
  async def all_handler(request):
      content = await request.content.read()
      strcontent = content.decode("ascii") # python 3.5 and lower do not accept bytes in json.loads
      parsedrequest = json.loads(strcontent)

      async def client_connected_tb(client_reader, client_writer):
          client_writer.write(content)
          await client_writer.drain()

          #client_writer.close()

          data = b""
          while True:
              data += await client_reader.read()
              try:
                  parsedresponse = json.loads(data.decode("ascii"))
                  assert parsedresponse["id"] == parsedrequest["id"], "mismatched response " + repr(parsedresponse) + " for request " + repr(parsedrequest)
                  break
              except:
                  await asyncio.sleep(0.1)
          await q.put(json.dumps(parsedresponse).encode("utf-8"))
      logging.info("waiting for server lock {}".format(port))
      async with locks[port]:
        server = await asyncio.start_server(client_connected_tb, port=port, backlog=1)
        resp = None
        while resp is None:
          try:
            resp = await asyncio.wait_for(q.get(), 30)
            assert resp, "Got None from queue!"
          except asyncio.TimeoutError: 
            logging.info("{} was not connected to!".format(port))
          else:
            server.close()
            await server.wait_closed()
            return web.Response(body=resp, content_type="application/json")

  app = web.Application()
  app.router.add_route("*", "", all_handler)
  return app.make_handler()

#Maps ports to queues
assoc = {}

class Queues:
    def __init__(self):
        self.readQueue = asyncio.Queue()
        self.writeQueue = asyncio.Queue()
        self.killQueue = asyncio.Queue()

def make_chain(offset, silent, simnet, testnet, lnddir):
  logging.info("starting chain on " + str(9090 + offset//5))
  coro = loop.create_server(make_h2handler(8433+offset, killQueuePort=8432+offset), '127.0.0.1', 9090+offset//5)
  elec1 = loop.create_server(mkhandler(8432+offset), '127.0.0.1', 8433+offset)

  assert 8432 + offset not in assoc
  assoc[8432 + offset] = Queues()

  writeQueue = assoc[8432+offset].writeQueue
  queueMonitor = socksserver.queueMonitor(assoc[8432+offset].readQueue, writeQueue, 8432+offset, assoc[8432+offset].killQueue)
  lnd = get_lnd_server(9090+offset//5, peerport=9735+offset//5, rpcport=10009+offset//5, restport=8081+offset//5, silent=silent, simnet=simnet, testnet=testnet, lnddir=lnddir)
  # return writeQueue as invoiceQueue
  return [lnd, coro, elec1, queueMonitor], writeQueue

PortPair = collections.namedtuple('PortPair', ['electrumReverseHTTPPort', 'lndRPCPort', 'lnddir'])

async def receiveStreamingUpdates(connStr, creds, prefix, subscriptionMessageClass, streamingRpcFunc, invoiceQueue):
    while True:
        try:
            channel = secure_channel(connStr, creds)
            mystub = lnrpcgrpc.LightningStub(channel)
            request = getattr(lnrpc, subscriptionMessageClass)()
            invoiceSource = getattr(mystub, streamingRpcFunc)(request)
            async for invoice in invoiceSource:
                logging.info("%s %s %s", prefix, streamingRpcFunc, invoice)
                await invoiceQueue.put(json_format.MessageToJson(invoice).encode("ascii") + b"\n")
        except grpc.RpcError as rpc_error:
            try:
                message = rpc_error.details()
            except AttributeError:
                message = str(rpc_error)
            #logging.info("sleeping, streaming update %s %s failed: %s", prefix, streamingRpcFunc, message)
            await asyncio.sleep(5)
            continue

class RealPortsSupplier:
    def __init__(self, simnet, testnet):
        self.currentOffset = 0
        self.keysToOffset = {}
        self.testnet = testnet
        self.simnet = simnet
    # returns keys for assoc
    async def get(self, socksKey):
        hexKey = binascii.hexlify(socksKey).decode("ascii")
        lnddir = "/tmp/lnd_" + hexKey
        print("HEXKEY", hexKey)
        # socksKey is the first 6 bytes of a private key hash
        if socksKey not in self.keysToOffset:
            if hexKey in KEY_TO_PORT:
                chosenPort = KEY_TO_PORT[hexKey]
                print("sockskey {} has port saved: {}".format(hexKey, chosenPort))
            else:
                while self.currentOffset in KEY_TO_PORT.values():
                    print("skipping offset", self.currentOffset)
                    self.currentOffset += 1
                chosenPort = self.currentOffset
            KEY_TO_PORT[hexKey] = chosenPort
            chain, invoiceQueue = make_chain(chosenPort * 5, True, self.simnet, self.testnet, lnddir)
            asyncio.ensure_future(asyncio.gather(*chain))
            self.keysToOffset[socksKey] = chosenPort

            for otherPort in PEERPORTS_TO_PUBKEYS.keys():
                otherPubkey = PEERPORTS_TO_PUBKEYS[otherPort]

                cmd = "~/go/bin/lncli --lnddir=" + lnddir + " --rpcserver=localhost:" + str(chosenPort + 10009) + " connect " + shlex.quote(otherPubkey + "@localhost:" + str(otherPort))
                for attempt_number in range(10):
                  proc = await asyncio.create_subprocess_shell(cmd=cmd, stdout=PIPE, stderr=PIPE)
                  try:
                    await asyncio.wait_for(proc.wait(), 5)
                    if proc.returncode != 0:
                        logging.error("connect failed for %d time: %s, cmd=%s", attempt_number, (await proc.stderr.read()).decode("utf-8"), cmd)
                        await asyncio.sleep(5)
                    else:
                        break
                  except asyncio.TimeoutError:
                    logging.error("timeouterror connect")

            cert = None
            while True:
              try:
                with open(lnddir + '/tls.cert', "rb") as fp:
                  cert = fp.read()
                break
              except FileNotFoundError:
                logging.info("tls cert {} not ready, waiting".format(socksKey))
                await asyncio.sleep(5)
            # https://github.com/LN-Zap/zap-desktop/issues/324#issuecomment-371355152
            os.environ["GRPC_SSL_CIPHER_SUITES"] = 'ECDHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES128-SHA256'
            creds = grpc.ssl_channel_credentials(cert)
            endpoint = 'ipv4:///127.0.0.1:' + str(chosenPort + 10009)
            asyncio.ensure_future(receiveStreamingUpdates(endpoint, creds, str(chosenPort), "InvoiceSubscription", "SubscribeInvoices", invoiceQueue))
            # 2018-02-26T12:42:08.652111 1 SubscribeChannelGraph channel_updates {
            #   chan_id: 1347846224522444800
            #   chan_point {
            #     funding_txid_bytes: "`E\251\030\376\224\354>\223bgy\240\266&6Wq\242\240T\373\210\177o\256\245\315\320\275\020\310"
            #   }
            #   capacity: 11951584
            #   routing_policy {
            #     time_lock_delta: 144
            #     fee_base_msat: 1000
            #     fee_rate_milli_msat: 1
            #   }
            #   advertising_node: "03e6b04d79aeba31c34334c72e6160be5ece0fcb6784f44cb1ba516d77fd700ee1"
            #   connecting_node: "02464bfaaae78b98268a6a6d7e8f6a110c60dd1293811d6029b11ee9edb4bbf869"
            # }
            # 
            # 2018-02-26T12:42:08.710398 1 SubscribeChannelGraph node_updates {
            #   identity_key: "02c1376e9814d2ba3176b2127c3f4fba6c90b9e5dcffaa5ad880eb8a5f0e50c6cf"
            #   alias: "02c1376e9814d2ba3176"
            # }
            #asyncio.ensure_future(receiveStreamingUpdates(endpoint, creds, str(self.currentOffset), "GraphTopologySubscription", "SubscribeChannelGraph"))
            #asyncio.ensure_future(receiveStreamingUpdates(endpoint, creds, str(self.currentOffset), "GetTransactionsRequest", "SubscribeTransactions"))

        return PortPair(electrumReverseHTTPPort=8432 + (self.keysToOffset[socksKey] * 5), lndRPCPort=10009 + self.keysToOffset[socksKey] , lnddir=lnddir)


loop = asyncio.get_event_loop()

if len(sys.argv) > 1:
    # simnet
    coinbaseAddress = sys.argv[1]
    simnet = True
    testnet = False
else:
    simnet = False
    testnet = True


realPortsSupplier = RealPortsSupplier(simnet, testnet)

if simnet:
    srv = asyncio.start_server(socksserver.make_handler(assoc, realPortsSupplier), '127.0.0.1', 1080)
    server = loop.run_until_complete(asyncio.gather(create_on_loop(loop, realPortsSupplier), srv, get_electrumx_server(), get_btcd_server(coinbaseAddress), get_bitcoind_server()))
else:
    srv = asyncio.start_server(socksserver.make_handler(assoc, realPortsSupplier), '0.0.0.0', 1080)
    server = loop.run_until_complete(asyncio.gather(create_on_loop(loop, realPortsSupplier), srv))

try:
  loop.run_forever()
except KeyboardInterrupt:
  with open("key_to_port","w") as w:
    w.write(json.dumps(KEY_TO_PORT))
  raise
