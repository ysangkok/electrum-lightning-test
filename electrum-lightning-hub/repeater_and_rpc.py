#!/usr/bin/env python

# this should repeat signing requests from lnd and launch lncli for rpc

import traceback
import sys
import asyncio
import websockets
import logging

logging.basicConfig(level=logging.DEBUG)

q = asyncio.PriorityQueue()
to_lnd = asyncio.Queue()

async def handle_conn(conn, path):
    global q
    global to_lnd
    try:
        while True:
            print(q.qsize())
            ack_needed = q.qsize() > 0
            if ack_needed:
                print("ACK NEEDED")
                prio, msg = await q.get()
                await conn.send(msg)
            recv_task = asyncio.ensure_future(conn.recv())
            def mark_recv_done(x):
                nonlocal recv_done
                recv_done = True
            tasks = [recv_task]
            recv_task.add_done_callback(mark_recv_done)
            queue_done = recv_done = False
            if not ack_needed:
                queue_task = asyncio.ensure_future(q.get())
                def mark_queue_done(x):
                    nonlocal queue_done
                    print("queue_DONE!!!!!!!!!!!!!!!!!!!!")
                    queue_done = True
                queue_task.add_done_callback(mark_queue_done)
                tasks.append(queue_task)
            print(tasks)
            try:
                done, pending = await asyncio.wait_for(asyncio.shield(asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)), 1.0)
            except asyncio.TimeoutError:
                for job in tasks: job.cancel()
                continue
            if not recv_done:
                print("recv NOT DONE")
            else:
                print("recv done")
            doneone = next(iter(done))
            if not ack_needed: assert len(list(pending)) == 1
            if ack_needed: assert len(list(pending)) == 0
            if queue_done and recv_done:
                await asyncio.gather(*tasks)
            for p in pending: p.cancel()
            if queue_done: assert not ack_needed
            if recv_done:
                if recv_task.exception():
                    print("BREAKING")
                    if ack_needed: await q.put((prio, msg))
                    break
                if recv_task.result().strip() == b"":
                    print("ignoring newline")
                    continue
                if q.qsize() > 0:
                    conn.send(b"ignoring msg, you need to reply")
                    continue
                assert q.qsize() == 0
                print("put for lnd")
                await to_lnd.put(recv_task.result())
            if queue_done:
                prio, msg = queue_task.result()
                await q.put((prio, msg))
                assert q.qsize() == 1
    except Exception:
        traceback.print_exc()
        globtask.cancel()

async def client():
    global globtask
    print(sys.argv[1])
    try:
        async with websockets.connect('ws://localhost:' + sys.argv[1]) as lnd:
            while True:
                async def f1():
                    req = await lnd.recv()
                    print("< {}".format(req))
                    await q.put((q.qsize(), req))
                async def f2():
                    rply = await to_lnd.get()
                    print("> {}".format(rply))
                    await lnd.send(rply)
                l = [asyncio.ensure_future(x()) for x in [f1, f2]]
                try:
                  done, pending = await asyncio.wait_for(asyncio.shield(asyncio.wait(l, return_when=asyncio.FIRST_COMPLETED)), 1.0)
                except asyncio.TimeoutError:
                  for job in l: job.cancel()
                  continue
                for p in pending: p.cancel()
                doneone = next(iter(done))
                if doneone.exception():
                    raise doneone.exception()
    except Exception:
        traceback.print_exc()
        globtask.cancel()

async def status():
    while True:
        print("q: {} to_lnd: {}".format(q.qsize(), to_lnd.qsize()))
        await asyncio.sleep(5)

server = websockets.serve(handle_conn, 'localhost', 8765)

loop = asyncio.get_event_loop()
loop.set_debug(True)
l = [server, status(), client()]
globtask = asyncio.ensure_future(asyncio.wait(l))
loop.run_until_complete(globtask)
loop.run_forever()
loop.close()
