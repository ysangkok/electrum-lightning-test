#!/usr/bin/env python3.5
import asyncio
import sys
import json
import logging

def make_handler(assoc, realPortsSupplier):
    async def handler(reader, writer):
        if (await reader.read(5)) != b"MAGIC":
            writer.close()
            return
        key = await reader.read(6)
        portPair = await realPortsSupplier.get(key)
        realPort = portPair.electrumReverseHTTPPort
        logging.info("socksserver using realport = {}".format(realPort))
        read, toWrite = assoc[realPort].readQueue, assoc[realPort].writeQueue

        while True:
            req = await toWrite.get()
            writer.write(req)
            try:
                await writer.drain()
            except:
                await toWrite.put(req)
                logging.info("error while draining")
                logging.info("put back on queue, queue now", toWrite.qsize())
                return

            data = b""

            answered = False
            while not reader.at_eof():
                newlines = sum(1 if x == b"\n"[0] else 0 for x in data)
                if newlines > 1: logging.warning("Too many newlines 1! %s", repr(data))
                try:
                    data += await asyncio.wait_for(reader.read(2048), 5)
                except asyncio.TimeoutError:
                    break
                try:
                    json.loads(data.decode("ascii"))
                except ValueError:
                    logging.warning("ValueError while loading: data: ", data)
                    continue
                else:
                    await read.put(data)
                    answered = True
                    break
            if not answered:
                await toWrite.put(req)
                logging.warning("incomplete data received: " + repr(data))
                logging.warning("put back on queue, queue now %d", toWrite.qsize())
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
                    writer.write(data)
                    await writer.drain()
                    writer.close()

            async def copyToSocks():
                data = b""
                while True:
                    newlines = sum(1 if x == b"\n"[0] else 0 for x in data)
                    if newlines > 1: logging.warning("Too many newlines 2! %s", repr(data))
                    try:
                        payload = json.loads(data.decode("ascii"))
                    except ValueError:
                        data += await reader.read(1)
                    else:
                        await writeQueue.put(data)
                        return
            job = asyncio.ensure_future(asyncio.gather(copyToSocks(), copyFromSocks()))
            await killQueue.get()
            job.cancel()

if __name__ == '__main__':
    loop = asyncio.get_event_loop()
    readQueue = asyncio.Queue()
    writeQueue = asyncio.Queue()
    srv = asyncio.start_server(make_handler(readQueue, writeQueue), '127.0.0.1', 1080)
    loop.run_until_complete(asyncio.gather(srv, queueMonitor(readQueue, writeQueue, int(sys.argv[1]))))
    loop.run_forever()
