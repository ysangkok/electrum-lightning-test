import jsonrpc_base
import asyncio
import io
import collections
from typing import List, Tuple

from h2.config import H2Configuration
from h2.connection import H2Connection
from h2.events import (
    ConnectionTerminated, DataReceived, RequestReceived, StreamEnded
)
from h2.errors import ErrorCodes
from h2.exceptions import ProtocolError

from lib.ln import rpc_pb2_grpc, rpc_pb2
from google.protobuf import json_format

import json

from jsonrpc_async import Server
import traceback
import json
import shlex
import tempfile
import os
import sys

from subprocess import DEVNULL

import socksserver

from aiohttp import web

from lncli_endpoint import create_on_loop

RequestData = collections.namedtuple('RequestData', ['headers', 'data'])

class H2Protocol(asyncio.Protocol):
    def __init__(self, elec):
        config = H2Configuration(client_side=False, header_encoding='utf-8')
        self.conn = H2Connection(config=config)
        self.transport = None
        self.stream_data = {}
        self.elec = elec

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
              asyncio.ensure_future(killQueue.put(data))
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
            asyncio.ensure_future(killQueue.put(data))
          asyncio.ensure_future(res).add_done_callback(done)
          return
      raise Exception("method " + path + " not found!")

servers = {}

def make_h2handler(port):
  def handler():
    if port in servers:
      return servers[port]
    servers[port] = H2Protocol(Server("http://localhost:" + str(port)))
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
    return asyncio.create_subprocess_shell(cmd, stdout=DEVNULL, stderr=DEVNULL)

async def get_lnd_server(electrumport, peerport, rpcport, restport, silent=True):
      datadir=tempfile.TemporaryDirectory(prefix="lnd_datadir")
      logdir=tempfile.TemporaryDirectory(prefix="lnd_logdir")
      kwargs = {"stdout":DEVNULL, "stderr":DEVNULL} if silent else {}
      lnd = await asyncio.create_subprocess_shell("/home/janus/go/bin/lnd --no-macaroons --configfile=/dev/null --rpcport=" + str(rpcport) + " --restport=" + str(restport) + " --logdir=" + logdir.name + " --datadir=" + datadir.name + " --peerport=" + str(peerport) + " --bitcoin.active --bitcoin.simnet --bitcoin.rpcuser=youruser --bitcoin.rpcpass=SomeDecentp4ssw0rd --bitcoin.rpchost=localhost:18556 --noencryptwallet --electrumport " + str(electrumport), **kwargs)
      return lnd

def mkhandler(port):
  q = asyncio.Queue()
  async def all_handler(request):
      content = await request.content.read()
      print("received request with method", json.loads(content)["method"])

      async def client_connected_tb(client_reader, client_writer):
          print("sent request with method", json.loads(content)["method"])
          client_writer.write(b"POST / HTTP/1.0\r\nContent-length: " + str(len(content)).encode("ascii") + b"\r\nContent-type: application/json\r\n\r\n" + content)
          await client_writer.drain()

          # these two lines could be after q.put
          #client_writer.close()

          while True:
              line = await client_reader.readline()
              print("read line", line)
              try:
                  parsed = json.loads(line.strip())
                  break
              except:
                  await asyncio.sleep(0.1)
          await q.put(json.dumps(parsed).encode("utf-8"))
      server = await asyncio.start_server(client_connected_tb, port=port, backlog=1)
      resp = await q.get()
      print("waiting for closed with resp", resp)
      server.close()
      await server.wait_closed()
      return web.Response(body=resp, content_type="application/json")
  app = web.Application()
  app.router.add_route("*", "", all_handler)
  return app.make_handler()

def make_chain(offset, silent=True):
  coro = loop.create_server(make_h2handler(8433+offset), '127.0.0.1', 9090+offset)
  elec1 = loop.create_server(mkhandler(8432+offset), '127.0.0.1', 8433+offset)
  lnd = get_lnd_server(9090+offset, peerport=9735+offset, rpcport=10009+offset, restport=8080+offset, silent=silent)
  return [coro, elec1, lnd]

coinbaseAddress = sys.argv[1]

loop = asyncio.get_event_loop()

readQueue = asyncio.Queue()
writeQueue = asyncio.Queue()
killQueue = asyncio.Queue()
srv = asyncio.start_server(socksserver.make_handler(readQueue, writeQueue), '127.0.0.1', 1080)

server = loop.run_until_complete(asyncio.gather(create_on_loop(loop), srv, socksserver.queueMonitor(readQueue, writeQueue, 8432, killQueue), *make_chain(0, False), get_electrumx_server(), get_btcd_server(coinbaseAddress), get_bitcoind_server()))
loop.run_forever()
