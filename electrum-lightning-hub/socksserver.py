#!/usr/bin/env python3.5
import socket
import asyncio
from struct import pack, unpack
import sys



def make_server_class(user_port_association):
  olddata = b""
  CLIENT_TRANSPORT = None
  SERVER_TRANSPORT = None
  class Server(asyncio.Protocol):
    INIT, HOST, DATA, AUTH = 0, 1, 2, 3
    def __init__(self):
        nonlocal CLIENT_TRANSPORT
        super(Server, self).__init__()
        self.task = None
        self.state = self.INIT
        self.transport = None

    def connection_made(self, transport):
        if self.transport: self.transport.close()
        print('from:', transport.get_extra_info('peername'))
        hostip, port = transport.get_extra_info('sockname')
        print((hostip, port))
        self.transport = transport

    def connection_lost(self, exc):
        nonlocal olddata
        olddata = b""
        self.transport.write_eof()
        self.transport.close()
        print("connection lost outer")
        if self.task:
            try:
                print("task exception", self.task.exception())
            except:
                pass
            self.task = None

    def data_received(self, data):
        nonlocal olddata, CLIENT_TRANSPORT
        # print('send:', repr(data))

        if olddata != b"":
            data = olddata + data
            olddata = b""
        if self.state == self.INIT:
            if len(data) < 9:
                olddata = data
                return
            if data[0] != 0x04:
                print("closing because first byte is not 0x04.", data)
                self.transport.close()
                return
            assert data[1] == 0x01
            port = int.from_bytes(data[2:4], byteorder="big")
            if port == 80:
                print("port 80 was asked for")
                self.transport.close()
                return
            hostname, nxt = socket.inet_ntop(socket.AF_INET, data[4:8]), 8

            print('IGNORING to:', hostname, port)
            self.newtask()
            self.transport.write(b"\x00\x5a" + (6*b"\x00"))
            self.state = self.DATA

        elif self.state == self.DATA:
            if len(data) == 0: return
            if CLIENT_TRANSPORT:
                CLIENT_TRANSPORT.write(data)
                CLIENT_TRANSPORT.write_eof()
                print("wrote", data)
            else:
                olddata = data
                print("no client transport")
    def newtask(self):
        self.task = asyncio.ensure_future(self.connect("localhost", user_port_association[b"donkey"]))
    def eof_received(self):
        print("eof received outer")
        #if CLIENT_TRANSPORT:
        #    CLIENT_TRANSPORT.close()
        self.transport.write_eof()
        self.transport.close()

    async def connect(outer, hostname, port):
        nonlocal CLIENT_TRANSPORT, SERVER_TRANSPORT
        class Client(asyncio.Protocol):
            def connection_made(self, transport):
                nonlocal SERVER_TRANSPORT
                print("connection made inner")
                self.transport = transport
                SERVER_TRANSPORT = None

            def eof_received(self):
                print("Client received eof!")
        
            def data_received(self, data):
                print('recv:', repr(data))
                SERVER_TRANSPORT.write(data)
        
            def connection_lost(self, *args):
                SERVER_TRANSPORT.write_eof()
                SERVER_TRANSPORT.close()
                print("connection lost inner")
        loop = asyncio.get_event_loop()
        transport, client = \
            await loop.create_connection(Client, hostname, port)
        print("connected to", port)
        SERVER_TRANSPORT = outer.transport
        CLIENT_TRANSPORT = transport
        outer.data_received(b"")
        #hostip, port = transport.get_extra_info('sockname')
        #host = unpack("!I", socket.inet_aton(hostip))[0]
  return Server

def make_server(user_port_association):
    loop = asyncio.get_event_loop()
    srv = loop.create_server(make_server_class(user_port_association), '127.0.0.1', 1080)
    return srv

if __name__ == '__main__':
    loop = asyncio.get_event_loop()
    srv = loop.create_server(make_server_class({b"donkey": sys.argv[1]}), '127.0.0.1', 1080)
    loop.run_until_complete(srv)
    loop.run_forever()
