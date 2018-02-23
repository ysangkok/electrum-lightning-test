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

from lib.ln import rpc_pb2_grpc, rpc_pb2

import lib.ln.lnrpc.rpc_pb2 as lnrpc
import lib.ln.lnrpc.rpc_pb2_grpc as lnrpcgpc

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
from subprocess import DEVNULL

import socksserver

from lncli_endpoint import create_on_loop

RequestData = collections.namedtuple('RequestData', ['headers', 'data'])

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
          print("probably already 405")
          # Just return, we probably 405'd this already
          return

      headers = request_data.headers
      body = request_data.data.getvalue()

      # ['FetchRootKey', 'ConfirmedBalance', 'ListUnspentWitness', 'NewAddress']
      methods = [x for x in rpc_pb2_grpc.ElectrumBridgeServicer.__dict__.keys() if not x.startswith("__")]

      path = dict(headers)[":path"]
      print("PATH:", path)

      for methodname in methods:
        if methodname in path:
          a = rpc_pb2.__dict__[methodname + "Request"]()
          # https://grpc.io/docs/guides/wire.html
          if body[0] != 0:
            print("compressed grpc message?")
            print("  ", body)
            print("  ", methodname + "Request")

          dec = int.from_bytes(body[1:5], byteorder="big")
          if dec + 5 != len(body):
            print("length mismatch?")
            print("  ", body[1:5], dec, len(body))
          a.ParseFromString(body[5:])
          jso = json_format.MessageToJson(a)
          if len(json.loads(jso).keys()) == 0 and methodname == "FetchInputInfo":
            raise Exception("no keys" + repr(a) + " " + repr(body))
          res = getattr(self.elec, methodname)(jso)
          def done(fut):
            try:
              fut.exception()
              print("result", fut.result())
            except Exception as e:
              print("While handling " + methodname)
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
    print("made new client connecting to port", port)
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

async def get_lnd_server(electrumport, peerport, rpcport, restport, silent, simnet, testnet, datadir):
      assert simnet and not testnet or not simnet and testnet
      logdir=tempfile.TemporaryDirectory(prefix="lnd_logdir")
      kwargs = {"stdout":DEVNULL, "stderr":DEVNULL} if silent else {}
      bitcoinrpcport = 18556 if simnet else 18334 # TODO does not support mainnet
      cmd = "~/go/bin/lnd --no-macaroons --debuglevel warn --configfile=/dev/null --rpclisten=localhost:" + str(rpcport) + " --restlisten=localhost:" + str(restport) + " --logdir=" + logdir.name + " --datadir=" + datadir + " --listen=localhost:" + str(peerport) + " --bitcoin.active " + ("--bitcoin.simnet" if simnet else "") + ("--bitcoin.testnet" if testnet else "") + " --btcd.rpcuser=youruser --btcd.rpcpass=SomeDecentp4ssw0rd --btcd.rpchost=localhost:" + str(bitcoinrpcport) + " --noencryptwallet --electrumport " + str(electrumport)
      print(cmd)
      lnd = await asyncio.create_subprocess_shell(cmd, **kwargs)
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
                  parsedresponse = json.loads(data.split(b"\n")[-1].decode("ascii"))
                  assert parsedresponse["id"] == parsedrequest["id"], "mismatched response " + repr(parsedresponse) + " for request " + repr(parsedrequest)
                  break
              except:
                  await asyncio.sleep(0.1)
          await q.put(json.dumps(parsedresponse).encode("utf-8"))
      print("waiting for server lock {}".format(port))
      async with locks[port]:
        server = await asyncio.start_server(client_connected_tb, port=port, backlog=1)
        resp = None
        while resp is None:
          try:
            resp = await asyncio.wait_for(q.get(), 5)
            assert resp, "Got None from queue!"
          except asyncio.TimeoutError: 
            print("{} was not connected to!".format(port))
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

def make_chain(offset, silent, simnet, testnet, datadir):
  print("starting chain on " + str(9090 + offset//5))
  coro = loop.create_server(make_h2handler(8433+offset, killQueuePort=8432+offset), '127.0.0.1', 9090+offset//5)
  elec1 = loop.create_server(mkhandler(8432+offset), '127.0.0.1', 8433+offset)

  assert 8432 + offset not in assoc
  assoc[8432 + offset] = Queues()

  queueMonitor = socksserver.queueMonitor(assoc[8432+offset].readQueue, assoc[8432+offset].writeQueue, 8432+offset, assoc[8432+offset].killQueue)
  lnd = get_lnd_server(9090+offset//5, peerport=9735+offset//5, rpcport=10009+offset//5, restport=8081+offset//5, silent=silent, simnet=simnet, testnet=testnet, datadir=datadir)
  return [lnd, coro, elec1, queueMonitor]

PortPair = collections.namedtuple('PortPair', ['electrumReverseHTTPPort', 'lndRPCPort', 'datadir'])

async def receiveStreamingUpdates(connStr, creds, prefix, subscriptionMessageClass, streamingRpcFunc):
    while True:
        try:
            channel = secure_channel(connStr, creds)
            mystub = lnrpcgrpc.LightningStub(channel)
            request = getattr(lnrpc, subscriptionMessageClass)()
            invoiceSource = getattr(mystub, streamingRpcFunc)(request)
            async for invoice in invoiceSource:
                print(datetime.now().isoformat(), prefix, streamingRpcFunc, invoice)
        except grpc.RpcError as rpc_error:
            try:
                message = rpc_error.details()
            except AttributeError:
                message = str(rpc_error)
            print("sleeping, streaming update", prefix, streamingRpcFunc, "failed:", message)
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
        datadir = "/tmp/lnd_datadir_" + binascii.hexlify(socksKey).decode("ascii")
        # socksKey is the first 6 bytes of a private key hash
        if socksKey not in self.keysToOffset:
            asyncio.ensure_future(asyncio.gather(*make_chain(self.currentOffset * 5, False, self.simnet, self.testnet, datadir)))
            self.currentOffset += 1
            chosenPort = self.currentOffset - 1
            self.keysToOffset[socksKey] = chosenPort

            with open(os.path.expanduser('~/.lnd/tls.cert'), "rb") as fp:
              cert = fp.read()
            creds = grpc.ssl_channel_credentials(cert)
            endpoint = 'ipv4:///127.0.0.1:' + str(chosenPort + 10009)
            asyncio.ensure_future(receiveStreamingUpdates(endpoint, creds, str(self.currentOffset), "InvoiceSubscription", "SubscribeInvoices"))
            asyncio.ensure_future(receiveStreamingUpdates(endpoint, creds, str(self.currentOffset), "GraphTopologySubscription", "SubscribeChannelGraph"))
            asyncio.ensure_future(receiveStreamingUpdates(endpoint, creds, str(self.currentOffset), "GetTransactionRequest", "SubscribeTransactions"))

        return PortPair(electrumReverseHTTPPort=8432 + (self.keysToOffset[socksKey] * 5), lndRPCPort=10009 + self.keysToOffset[socksKey] , datadir=datadir)


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

loop.run_forever()
