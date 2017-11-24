#!/usr/bin/env python3.5
import socket
import asyncio
from struct import pack, unpack
import sys
import traceback
from queue import Queue
import json
import async_timeout
import concurrent.futures

queue = asyncio.Queue()

def make_handler(assoc, realPortsSupplier):
    async def handler(reader, writer):
        data = await reader.read(1)
        assert len(data) == 1
        if data[0] != 0x04:
            print("closing because first byte is not 0x04.", data)
            writer.close()
            return
        data = await reader.read(1)
        assert len(data) == 1
        assert data[0] == 0x01
        portBytes = await reader.read(2)
        port = int.from_bytes(portBytes, byteorder="big")
        if port == 80:
            print("port 80 was asked for: " + repr(data))
            writer.close()
            return
        ipBytes = await reader.read(4)
        hostname = socket.inet_ntop(socket.AF_INET, ipBytes)

        portPair = await realPortsSupplier.get(ipBytes + portBytes)
        realPort = portPair.electrumReverseHTTPPort
        read, toWrite = assoc[realPort].readQueue, assoc[realPort].writeQueue

        #print('IGNORING to:', hostname, port)
        writer.write(b"\x00\x5a" + (6*b"\x00"))
        await writer.drain()

        data = await reader.read(1)
        if data != b"\x00":
            print("unexpected nonnull: " + repr(data))
            writer.close()
            return

        req = await toWrite.get()
        writer.write(req)
        await writer.drain()

        data = b""

        with async_timeout.timeout(3):
          while not reader.at_eof():
              try:
                  data += await reader.read()
              except ConnectionResetError:
                  print("connection reset")
                  break
              except concurrent.futures.CancelledError:
                  print("cancelled. timeout?")
                  break
        try:
            json.loads(data.split(b"\n")[-1].decode("ascii"))
        except ValueError:
            await toWrite.put(req)
            print("incomplete data: " + repr(data))
            print("put back on queue, queue now", toWrite.qsize())
        else:
            await read.put(data)
            writer.close()
            print("put response")
    return handler

async def queueMonitor(readQueue, writeQueue, port, killQueue):
    while True:
        try:
            reader, writer = await asyncio.open_connection('localhost', port)
        except ConnectionRefusedError:
            await asyncio.sleep(1)
            continue
        else:
            async def copyFromSocks():
                while True:
                    data = await readQueue.get()
                    print("sending", data)
                    writer.write(data)
                    await writer.drain()
                    writer.close()

            async def copyToSocks():
                data = b""
                while True:
                    try:
                        payload = json.loads(data.split(b"\n")[-1].decode("ascii"))
                    except ValueError:
                        data += await reader.read(1)
                    else:
                        await writeQueue.put(data)
                        return
            job = asyncio.ensure_future(asyncio.gather(copyToSocks(), copyFromSocks()))
            print("killQueue", await killQueue.get())
            job.cancel()

if __name__ == '__main__':
    loop = asyncio.get_event_loop()
    readQueue = asyncio.Queue()
    writeQueue = asyncio.Queue()
    srv = asyncio.start_server(make_handler(readQueue, writeQueue), '127.0.0.1', 1080)
    loop.run_until_complete(asyncio.gather(srv, queueMonitor(readQueue, writeQueue, int(sys.argv[1]))))
    loop.run_forever()
