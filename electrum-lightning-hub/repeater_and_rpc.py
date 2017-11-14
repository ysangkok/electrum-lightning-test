import asyncio
import io
import ssl
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
import traceback

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
              print("result", fut.exception(), fut.result())
            except Exception as e:
              print("While handling " + methodname)
              traceback.print_exc()
              response_headers = (
                  (':status', '500'),
                  ('server', 'asyncio-h2'),
              )
              self.conn.send_headers(stream_id, response_headers, end_stream=True)
              self.transport.write(self.conn.data_to_send())
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
            self.transport.write(self.conn.data_to_send())
          asyncio.ensure_future(res).add_done_callback(done)
          return
      raise Exception("method " + path + " not found!")

import asyncio
from jsonrpc_async import Server

servers = {}

def make_h2handler(port):
  def handler():
    if port in servers:
      return servers[port]
    servers[port] = H2Protocol(Server("http://localhost:" + str(port)))
    print("made new client connecting to port", port)
    return servers[port]
  return handler

from aiohttp import web
import aiohttp
import aiohttp.client_exceptions
import aiohttp.client_reqrep
import traceback
import async_timeout

from distutils.version import StrictVersion
assert StrictVersion(aiohttp.__version__) >= StrictVersion("2.3.2")

loop = asyncio.get_event_loop()

def streamWriterToStreamResponse(streamWriter, request, lock):
    class MyResponse(web.Response):
        @property
        def transport(self):
            return streamWriter.transport
        def __init__(self):
            super(MyResponse, self).__init__()
            self.prepare(request)
            self._payload_writer = streamWriter
        def set_tcp_nodelay(self, val):
            pass
        def release(self):
            lock.release()
    resp = MyResponse()
    resp.available = True
    return resp

class MyProtocol(aiohttp.client_proto.ResponseHandler):
    def __init__(self, client_reader, client_writer, request, lock):
        super(MyProtocol, self).__init__()
        self.request = request
        self.writer = streamWriterToStreamResponse(client_writer, request, lock)
        self._loop = loop

import json

class ServerConnector(aiohttp.BaseConnector):
    def __init__(self, port, request, replylock):
        self.replylock = replylock
        aiohttp.BaseConnector.__init__(self)
        self.port = port
        self.request = request
        self.client_connected = asyncio.Lock()
        self.reply = None
    async def _create_connection(self, req):
        async def client_connected_tb(client_reader, client_writer):
            print("client connected")
            lock = asyncio.Lock()
            await lock.acquire()
            self._protocol = MyProtocol(client_reader, client_writer, self.request, lock)
            self.client_connected.release()
            try:
                with async_timeout.timeout(3): # electrum must answer within 3 seconds
                    await lock.acquire()
                    parsed = None
                    while True:
                        line = await client_reader.readline()
                        try:
                            parsed = json.loads(line.strip())
                            break
                        except:
                            await asyncio.sleep(0.1)
                    self.reply = json.dumps(parsed).encode("utf-8")
            except asyncio.TimeoutError:
                self.reply = None
            self._protocol.data_received(b"HTTP/1.0 200 OK\r\n\r\n")
            self.replylock.release()
            #self._protocol.feed_data(repl)
            #self._protocol.feed_eof()
        server = await asyncio.start_server(client_connected_tb, port=self.port)
        self.server = server
        await self.client_connected.acquire()
        return self._protocol

async def get_bitcoind_server():
      bitcoind = await asyncio.create_subprocess_shell("rm -rf /home/janus/.bitcoin/regtest && /home/janus/bitcoin-simnet/bin/bitcoind", stdout=subprocess.PIPE, stderr=subprocess.PIPE)
      return bitcoind

import shlex
import tempfile
import os

def get_btcd_server():
      t = tempfile.NamedTemporaryFile(prefix="btcd_config", delete=False)
      t.write(b"""
        rpcuser=youruser
        rpcpass=SomeDecentp4ssw0rd
        simnet=1
        miningaddr=SjBcfBCzeAHBCoWiGrwR7Emw4uoRUvKAfY
        txindex=1
        addrindex=1
        rpclisten=127.0.0.1
      """)
      t.flush()
      datadir=tempfile.TemporaryDirectory(prefix="btcd_datadir")
      # Note that ~/.btcd is still used for e.g. the rpccert!
      cmd = "/home/janus/go/bin/btcd -C " + shlex.quote(t.name) + " --datadir " + shlex.quote(datadir.name) + " --connect localhost"
      return asyncio.create_subprocess_shell(cmd)#, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

import subprocess

async def get_lnd_server(electrumport, peerport, rpcport, restport):
      datadir=tempfile.TemporaryDirectory(prefix="lnd_datadir")
      logdir=tempfile.TemporaryDirectory(prefix="lnd_logdir")
      lnd = await asyncio.create_subprocess_shell("/home/janus/go/bin/lnd --no-macaroons --configfile=/dev/null --rpcport=" + str(rpcport) + " --restport=" + str(restport) + " --logdir=" + logdir.name + " --datadir=" + datadir.name + " --peerport=" + str(peerport) + " --bitcoin.active --bitcoin.simnet --bitcoin.rpcuser=youruser --bitcoin.rpcpass=SomeDecentp4ssw0rd --bitcoin.rpchost=localhost:18556 --noencryptwallet --electrumport " + str(electrumport))
      return lnd

def mkhandler(port):
  async def all_handler(request):
      print("all handler {}".format(port))
      content = await request.content.read()
      while True:
          replylock = asyncio.Lock()
          await replylock.acquire()
          connector = ServerConnector(port, request, replylock)
          await connector.client_connected.acquire()
          try:
              with async_timeout.timeout(30):
                  async with aiohttp.ClientSession(connector=connector) as session:
                      # TODO replace replylock with reply coming through response
                      async with session.post("http://localhost:" + str(port) + str(request.rel_url), data=content) as response: # TODO butcher relurl
                          #body = await response.text()
                          #print("response received", body)
                          await replylock.acquire()
                          print("replylock acquired")
                          if connector.reply: return web.Response(body=connector.reply, content_type="application/json")
                          else: return web.Response(status=500)
          except asyncio.TimeoutError:
              print("timeout, retrying")
              connector.server.close()
              await connector.server.wait_closed()
              if connector.reply:
                  return web.Response(body=connector.reply, content_type="application/json")
          finally:
              if connector.server:
                  connector.server.close()
                  await connector.server.wait_closed()
  app = web.Application()
  app.router.add_route("*", "", all_handler)
  return app.make_handler()

def make_chain(offset):
  coro = loop.create_server(make_h2handler(8433+offset), '127.0.0.1', 9090+offset)
  elec1 = loop.create_server(mkhandler(8432+offset), '127.0.0.1', 8433+offset)
  lnd = get_lnd_server(9090+offset, peerport=9735+offset, rpcport=10009+offset, restport=8080+offset)
  return [coro, elec1, lnd]

server = loop.run_until_complete(asyncio.gather(*make_chain(0), *make_chain(5), get_btcd_server(), get_bitcoind_server()))
loop.run_forever()
